package identity

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/net/idna"
	"golang.org/x/sync/singleflight"

	"github.com/vantigo-io/vantigo/server/internal/identity/store"
)

// The assignment of brands to people (brands design B7, B8, B10, B12): the
// first rule that matches a person decides their brand, a person matched by
// several may choose among them, and a person matched by none sees the default.
// Resolution is computed on every request from current data and never stored;
// the one thing it caches is what it asks Staff (entityCache).

// Rule kinds (B8) and the sources a resolution reports (B7).
const (
	ruleKindDomain      = "domain"
	ruleKindLegalEntity = "legal_entity"
	ruleKindAccessGroup = "access_group"

	sourceChoice  = "choice"
	sourceRule    = "rule"
	sourceDefault = "default"
)

// entityCacheTTL is how long the legal entities a person works under are kept
// (B18): a publish makes every open session refetch its brand, and none of
// those requests may become a query to Staff.
const entityCacheTTL = 30 * time.Second

// entityCacheMax bounds the cache; past it the expired entries are swept.
const entityCacheMax = 4096

// liveRule is a rule resolution reads: one pointing at a published brand.
type liveRule = store.ListLiveBrandRulesRow

// resolution is what the assignment makes of one person.
type resolution struct {
	brand   brandRow
	version brandVersion
	source  string
	// rule is the first rule that matches the person, if any: the one that
	// decides unless they chose. It is set whatever the source.
	rule *liveRule
	// candidates are the brands of every matching rule in the list's order, or
	// the default brand alone when no rule matches (B12).
	candidates []brandRow
	// choiceIgnored says the person has a choice that is no longer a candidate.
	choiceIgnored bool
	// facts of the person, for the lookup.
	displayName string
	emailDomain string
	// campaign is the brand's campaign in force, whose overlay resolveLook has
	// folded into version, and validUntil the next time that changes (B24).
	campaign   *campaignRow
	validUntil *time.Time
}

// entityCache remembers, per person, the legal entities Staff says they work
// under, for entityCacheTTL, and shares one in-flight read among the requests
// that ask for the same person at once.
type entityCache struct {
	mu     sync.Mutex
	m      map[uuid.UUID]entityEntry
	flight singleflight.Group
}

type entityEntry struct {
	ids     []string
	expires time.Time
}

// entitiesOf is the legal entity ids of userID's employments in force today:
// from the cache, else from Staff. It fails when Staff cannot be read — it
// never answers "none" for "could not tell". Without Staff it is none.
func (s *server) entitiesOf(ctx context.Context, userID uuid.UUID) ([]string, error) {
	if s.deps.BrandTargets == nil {
		return nil, nil
	}
	c := &s.entities
	now := s.deps.Clock()
	c.mu.Lock()
	if e, ok := c.m[userID]; ok && now.Before(e.expires) {
		c.mu.Unlock()
		s.metrics.entityReads.Add(ctx, 1, resultAttr("cache"))
		return e.ids, nil
	}
	c.mu.Unlock()
	v, err, _ := c.flight.Do(userID.String(), func() (any, error) {
		// A read shared by several requests must not die with the first one's
		// context.
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		got, err := s.deps.BrandTargets.LegalEntitiesOf(rctx, []uuid.UUID{userID})
		if err != nil {
			return nil, fmt.Errorf("identity: read the legal entities of a user: %w", err)
		}
		s.metrics.entityReads.Add(ctx, 1, resultAttr("staff"))
		ids := got[userID]
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.m == nil {
			c.m = map[uuid.UUID]entityEntry{}
		}
		if len(c.m) >= entityCacheMax {
			for k, e := range c.m {
				if !s.deps.Clock().Before(e.expires) {
					delete(c.m, k)
				}
			}
		}
		c.m[userID] = entityEntry{ids: ids, expires: s.deps.Clock().Add(entityCacheTTL)}
		return ids, nil
	})
	if err != nil {
		return nil, err
	}
	return v.([]string), nil
}

// normalizedDomain is the domain of an address, lower case and in punycode, as
// a domain rule's target is written ("" for an address without one).
func normalizedDomain(email string) string {
	at := strings.LastIndex(email, "@")
	if at < 0 {
		return ""
	}
	d := strings.ToLower(strings.TrimSuffix(email[at+1:], "."))
	if ascii, err := idna.Lookup.ToASCII(d); err == nil {
		return ascii
	}
	return d
}

// resolveUser is the brand a person sees and why (B7, B10), with their
// candidates (B12). It is the one function the resolved brand, the stylesheet,
// the lookup and the personal choice all go through.
func (s *server) resolveUser(ctx context.Context, userID uuid.UUID) (resolution, error) {
	def, err := s.defaultBrand(ctx)
	if err != nil {
		return resolution{}, err
	}
	res := resolution{brand: def, version: publishedOrBuiltin(def), source: sourceDefault, candidates: []brandRow{def}}
	if userID == uuid.Nil {
		return res, nil
	}
	facts, err := s.q.GetBrandResolutionFacts(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return res, nil
	}
	if err != nil {
		return resolution{}, fmt.Errorf("identity: read a user's brand facts: %w", err)
	}
	res.displayName = facts.DisplayName
	res.emailDomain = normalizedDomain(facts.NormalizedEmail)
	if facts.Kind != "person" { // an integration, an agent: the default (B8)
		return res, nil
	}
	rules, err := s.q.ListLiveBrandRules(ctx)
	if err != nil {
		return resolution{}, fmt.Errorf("identity: read the brand rules: %w", err)
	}
	var (
		groups     map[uuid.UUID]bool
		entities   map[string]bool
		matched    []liveRule
		candidates []brandRow
		seen       = map[uuid.UUID]bool{}
	)
	for _, r := range rules {
		ok := false
		switch r.Kind {
		case ruleKindDomain:
			ok = res.emailDomain != "" && r.TargetRef == res.emailDomain
		case ruleKindAccessGroup:
			if groups == nil {
				ids, err := s.q.ListUserActiveGroupIDs(ctx, userID)
				if err != nil {
					return resolution{}, fmt.Errorf("identity: read a user's groups: %w", err)
				}
				groups = make(map[uuid.UUID]bool, len(ids))
				for _, id := range ids {
					groups[id] = true
				}
			}
			if id, err := uuid.Parse(r.TargetRef); err == nil {
				ok = groups[id]
			}
		case ruleKindLegalEntity:
			if s.deps.BrandTargets == nil { // Staff is off: inert, never "no entity" (B9)
				continue
			}
			if entities == nil {
				ids, err := s.entitiesOf(ctx, userID)
				if err != nil {
					return resolution{}, err
				}
				entities = make(map[string]bool, len(ids))
				for _, id := range ids {
					entities[id] = true
				}
			}
			ok = entities[r.TargetRef]
		}
		if !ok {
			continue
		}
		matched = append(matched, r)
		if seen[r.BrandID] {
			continue
		}
		seen[r.BrandID] = true
		b, found, err := s.loadBrand(ctx, s.q, r.BrandID)
		if err != nil {
			return resolution{}, err
		}
		if !found || b.pub == nil {
			continue // impossible: the rule joined a published brand a moment ago
		}
		candidates = append(candidates, b)
	}
	if len(matched) == 0 || len(candidates) == 0 {
		// No rule matched: the default is the only candidate, and a choice is
		// valid only if it is the default itself.
		return s.applyChoice(res, facts.PreferredBrandID), nil
	}
	first := matched[0]
	res.rule = &first
	res.candidates = candidates
	res.brand, res.version, res.source = candidates[0], *candidates[0].pub, sourceRule
	for _, c := range candidates {
		if c.ID == first.BrandID {
			res.brand, res.version = c, *c.pub
		}
	}
	return s.applyChoice(res, facts.PreferredBrandID), nil
}

// applyChoice lets a person's own choice beat the rules while it is one of their
// candidates (B12), and reports it ignored when it is not.
func (s *server) applyChoice(res resolution, preferred *uuid.UUID) resolution {
	if preferred == nil {
		return res
	}
	for _, c := range res.candidates {
		if c.ID == *preferred {
			res.brand, res.version, res.source = c, publishedOrBuiltin(c), sourceChoice
			return res
		}
	}
	res.choiceIgnored = true
	return res
}

func publishedOrBuiltin(b brandRow) brandVersion {
	if b.pub == nil {
		return builtinBrand()
	}
	return *b.pub
}

// resolve is the brand a person sees. The stylesheet and the public look go
// through it; uuid.Nil is no one, and sees the default.
func (s *server) resolve(ctx context.Context, userID uuid.UUID) (brandRow, brandVersion, error) {
	res, err := s.resolveLook(ctx, userID)
	if err != nil {
		return brandRow{}, brandVersion{}, err
	}
	return res.brand, res.version, nil
}
