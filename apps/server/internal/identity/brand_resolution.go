package identity

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/identity/gen"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
)

// resolvedFor is the payload of B36 for a resolution: the brand a person sees,
// why, and the brands they may choose among (B12).
func (s *server) resolvedFor(res resolution) gen.ResolvedBrand {
	payload := s.resolvedBrand(res.brand, res.version, false)
	payload.Source = gen.ResolvedBrandSource(res.source)
	cands := make([]gen.BrandCandidate, 0, len(res.candidates))
	for _, c := range res.candidates {
		cands = append(cands, gen.BrandCandidate{Id: c.ID, Name: publishedOrBuiltin(c).Name})
	}
	payload.Candidates = &cands
	payload.ChoiceIgnored = &res.choiceIgnored
	return payload
}

// GetIdentityBrandsResolution Who sees which brand
// (GET /api/v1/identity/brands/resolution)
//
// For one person: the brand, the source (their own choice, the rule that
// matched or the default) and the candidates (B11). It names no employment
// detail — of the person's groups and legal entities only the rule that matched
// — unless the caller also holds the Staff permission that reads employment
// data and Staff is enabled.
func (s *server) GetIdentityBrandsResolution(ctx context.Context, req gen.GetIdentityBrandsResolutionRequestObject) (gen.GetIdentityBrandsResolutionResponseObject, error) {
	p, err := callerFrom(ctx)
	if err != nil {
		return nil, err
	}
	var userID uuid.UUID
	switch {
	case req.Params.UserId != nil && req.Params.Email == nil:
		userID = uuid.UUID(*req.Params.UserId)
	case req.Params.Email != nil && req.Params.UserId == nil:
		u, err := s.q.GetUserByNormalizedEmail(ctx, normalizeEmail(*req.Params.Email))
		if errors.Is(err, pgx.ErrNoRows) {
			return gen.GetIdentityBrandsResolution404Response{}, nil
		}
		if err != nil {
			return nil, fmt.Errorf("identity: look up a person by address: %w", err)
		}
		userID = u.ID
	default:
		return gen.GetIdentityBrandsResolution400JSONResponse(authErrorBody(codeBrandNotAccepted,
			"Give exactly one of userId and email.", map[string][]string{"userId": {"Give exactly one of userId and email."}})), nil
	}
	if _, err := s.q.GetBrandResolutionFacts(ctx, userID); errors.Is(err, pgx.ErrNoRows) {
		return gen.GetIdentityBrandsResolution404Response{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("identity: read a user: %w", err)
	}
	res, err := s.resolveUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	targets, err := s.readTargets(ctx, res.rule != nil && res.rule.Kind == ruleKindLegalEntity)
	if err != nil {
		return nil, err
	}
	out := gen.BrandResolution{
		UserId: userID, DisplayName: res.displayName, Source: gen.BrandResolutionSource(res.source),
		Brand:         gen.BrandRef{Id: res.brand.ID, Name: publishedOrBuiltin(res.brand).Name},
		Candidates:    make([]gen.BrandCandidate, 0, len(res.candidates)),
		ChoiceIgnored: res.choiceIgnored,
	}
	for _, c := range res.candidates {
		out.Candidates = append(out.Candidates, gen.BrandCandidate{Id: c.ID, Name: publishedOrBuiltin(c).Name})
	}
	if res.rule != nil {
		label, _, _ := targets.describe(res.rule.Kind, res.rule.TargetRef)
		position := int32(1)
		if rules, err := s.q.ListLiveBrandRules(ctx); err == nil {
			for i, r := range rules {
				if r.ID == res.rule.ID {
					position = int32(i + 1)
				}
			}
		}
		out.Rule = &gen.BrandResolutionRule{
			Id: res.rule.ID, Kind: gen.BrandResolutionRuleKind(res.rule.Kind), TargetRef: res.rule.TargetRef, TargetLabel: label, Position: position,
		}
	}
	// The fuller explanation reveals employment: only for a caller who also may
	// read it in Staff, and only while Staff is on.
	if s.deps.BrandTargets != nil {
		holds, err := s.access.UserHolds(ctx, p.UserID, permStaffView)
		if err != nil {
			return nil, err
		}
		if holds {
			details, err := s.resolutionDetails(ctx, userID, res)
			if err != nil {
				return nil, err
			}
			out.Details = &details
		}
	}
	return gen.GetIdentityBrandsResolution200JSONResponse(out), nil
}

func (s *server) resolutionDetails(ctx context.Context, userID uuid.UUID, res resolution) (gen.BrandResolutionDetails, error) {
	groups, err := s.groupNamesOf(ctx, userID)
	if err != nil {
		return gen.BrandResolutionDetails{}, err
	}
	ids, err := s.entitiesOf(ctx, userID)
	if err != nil {
		return gen.BrandResolutionDetails{}, err
	}
	names := []string{}
	if len(ids) > 0 {
		t, err := s.readTargets(ctx, true)
		if err != nil {
			return gen.BrandResolutionDetails{}, err
		}
		for _, id := range ids {
			if e, ok := t.entities[id]; ok {
				names = append(names, e.Name)
			}
		}
	}
	if groups == nil {
		groups = []string{}
	}
	return gen.BrandResolutionDetails{EmailDomain: res.emailDomain, AccessGroups: groups, LegalEntities: names}, nil
}

// PutIdentityAccountBrand Choose my brand (PUT /api/v1/identity/account/brand)
func (s *server) PutIdentityAccountBrand(ctx context.Context, req gen.PutIdentityAccountBrandRequestObject) (gen.PutIdentityAccountBrandResponseObject, error) {
	p, err := callerFrom(ctx)
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return gen.PutIdentityAccountBrand400JSONResponse(authErrorBody(codeBrandNotAccepted, "The request needs a body.", nil)), nil
	}
	res, err := s.resolveUser(ctx, p.UserID)
	if err != nil {
		return nil, err
	}
	var choice *uuid.UUID
	if req.Body.BrandId != nil {
		id := uuid.UUID(*req.Body.BrandId)
		choice = &id
		candidate := false
		for _, c := range res.candidates {
			candidate = candidate || c.ID == id
		}
		if !candidate {
			return gen.PutIdentityAccountBrand409JSONResponse(authErrorBody("brand_not_candidate",
				"That brand is not one of the brands you are assigned.", nil)), nil
		}
	}
	// A preference, like the colour scheme: written and announced, not audited
	// (B34). Only the person's own sessions hear it.
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := store.New(tx)
		if err := q.SetUserPreferredBrand(ctx, store.SetUserPreferredBrandParams{BrandID: choice, ID: p.UserID}); err != nil {
			return fmt.Errorf("identity: save a brand choice: %w", err)
		}
		return notifyBrandChosen(ctx, tx, p.UserID)
	})
	if err != nil {
		return nil, err
	}
	result := "cleared"
	if choice != nil {
		result = "chosen"
	}
	s.metrics.choices.Add(ctx, 1, resultAttr(result))
	res, err = s.resolveUser(ctx, p.UserID)
	if err != nil {
		return nil, err
	}
	return gen.PutIdentityAccountBrand200JSONResponse(s.resolvedFor(res)), nil
}
