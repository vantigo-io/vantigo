package staff

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is a group's sections, one per provider in the
// IdentityProvisioner slot (Entra design D10, plan task 3a, reading 3): the
// traits the provider declares — Staff knows no trait by name, and renders
// and checks them from the declaration alone — the values Staff stores, the
// switch HR turns the provider on and off with, and where the accounts
// worker's sync stands. The declaration is the provider's answer from its own
// state, asked on no connection of this module's and under the worker's call
// timeout; the values are checked against it when they are saved (required,
// choices, types, lengths), and the provider's own rules when the worker
// sends them.

// The trait types a declaration names (contracts.GroupTrait.Type). A value
// refused is a field error on values.<key> whose message is the code — the
// provider's own, contracts.TraitRequired or contracts.TraitInvalid, so the
// page words it as it words the section's lastError; a key the provider does
// not declare is invalid.
const (
	traitTypeBool   = "bool"
	traitTypeString = "string"
	traitTypeChoice = "choice"
	traitTypeList   = "list"
)

// invalidGroupTraitsTitle is the title of a section's validation problems.
const invalidGroupTraitsTitle = "Invalid group traits"

// providerSection is what a provider answered for a group's section: its
// info (name and status) and its declaration, ok false when it could not
// declare.
type providerSection struct {
	info   contracts.ProviderInfo
	traits []contracts.GroupTrait
	ok     bool
}

// askProvider asks p for its info and its declaration, each under the call
// timeout, on no connection of this module's. A provider that cannot say
// its status, or cannot declare, is failing with no traits.
func (s *server) askProvider(ctx context.Context, p contracts.IdentityProvisioner) providerSection {
	key := p.Key()
	info, err := withCallTimeout(ctx, func(ctx context.Context) (contracts.ProviderInfo, error) { return s.providerInfo(ctx, p) })
	if err != nil {
		s.logger().WarnContext(ctx, "staff: an identity provider could not say its status", "provider", key, "error", err.Error())
		info = contracts.ProviderInfo{Status: contracts.ProviderFailing}
	}
	info.Key = key
	if info.Name == "" {
		info.Name = key
	}
	traits, err := withCallTimeout(ctx, func(ctx context.Context) ([]contracts.GroupTrait, error) { return s.providerGroupTraits(ctx, p) })
	if err != nil {
		s.logger().WarnContext(ctx, "staff: an identity provider could not declare its group traits", "provider", key, "error", err.Error())
		info.Status = contracts.ProviderFailing
		return providerSection{info: info}
	}
	return providerSection{info: info, traits: traits, ok: true}
}

// traitText is a provider's text in the contract's shape: English, and
// Norwegian when the provider gave it.
func traitText(m map[string]string) gen.StaffGroupTraitText {
	out := gen.StaffGroupTraitText{En: m["en"]}
	if nb := m["nb"]; nb != "" {
		out.Nb = &nb
	}
	return out
}

// traitDeclaration is one declared trait as the contract answers it.
func traitDeclaration(t contracts.GroupTrait) gen.StaffGroupTraitDeclaration {
	out := gen.StaffGroupTraitDeclaration{
		Key: t.Key, Type: gen.StaffGroupTraitDeclarationType(t.Type), Label: traitText(t.Label),
		Default: t.Default, Required: t.Required,
	}
	if len(t.Description) > 0 {
		d := traitText(t.Description)
		out.Description = &d
	}
	if t.Type == traitTypeChoice || t.Type == traitTypeList {
		choices := mapRows(t.Choices, func(c contracts.TraitChoice) gen.StaffGroupTraitChoice {
			return gen.StaffGroupTraitChoice{Value: c.Value, Label: traitText(c.Label)}
		})
		out.Choices = &choices
	}
	if t.DocURL != "" {
		u := t.DocURL
		out.DocUrl = &u
	}
	if t.MaxLength > 0 {
		n := int32(t.MaxLength)
		out.MaxLength = &n
	}
	return out
}

// sectionResponse is a group's section under a provider: the provider's
// answer and the row, nil when HR never turned the provider on.
func sectionResponse(p providerSection, row *store.StaffGroupTrait) (gen.StaffGroupProvider, error) {
	out := gen.StaffGroupProvider{
		Key: p.info.Key, Name: p.info.Name, Status: p.info.Status,
		Traits: mapRows(p.traits, traitDeclaration), Values: map[string]interface{}{},
	}
	if out.Traits == nil {
		out.Traits = []gen.StaffGroupTraitDeclaration{}
	}
	if row == nil {
		return out, nil
	}
	if len(row.Values) > 0 {
		if err := json.Unmarshal(row.Values, &out.Values); err != nil {
			return out, fmt.Errorf("staff: read group %d's values under %s: %w", row.GroupID, row.Provider, err)
		}
		if out.Values == nil {
			out.Values = map[string]interface{}{}
		}
	}
	state := gen.StaffGroupSyncState(row.State)
	out.Enabled, out.State = row.Enabled, &state
	out.ExternalId, out.LastError, out.LastErrorField = row.ExternalID, row.LastError, row.LastErrorField
	out.SyncedAt, out.RetiredAt = row.SyncedAt, row.RetiredAt
	return out, nil
}

// checkTraitValues checks values against the declaration: every key
// declared, every value of its trait's type, among its choices and within
// its length, and, when enabled, every required trait given. It answers the
// values as they are stored — a null left out — and the field errors on
// values.<key>, each message the code.
func checkTraitValues(declared []contracts.GroupTrait, values map[string]any, enabled bool) (map[string]any, fieldErrors) {
	var f fieldErrors
	out := map[string]any{}
	byKey := map[string]contracts.GroupTrait{}
	for _, t := range declared {
		byKey[t.Key] = t
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		v := values[k]
		if v == nil {
			continue
		}
		t, ok := byKey[k]
		if !ok || !traitValueValid(t, v) {
			f.addCoded("values."+k, contracts.TraitInvalid, contracts.TraitInvalid)
			continue
		}
		out[k] = v
	}
	if enabled {
		for _, t := range declared {
			if t.Required && traitValueEmpty(out[t.Key]) {
				if _, refused := f.errs["values."+t.Key]; !refused {
					f.addCoded("values."+t.Key, contracts.TraitRequired, contracts.TraitRequired)
				}
			}
		}
	}
	return out, f
}

// traitValueValid is whether v is a value t admits.
func traitValueValid(t contracts.GroupTrait, v any) bool {
	admits := func(s string) bool {
		return slices.ContainsFunc(t.Choices, func(c contracts.TraitChoice) bool { return c.Value == s })
	}
	switch t.Type {
	case traitTypeBool:
		_, ok := v.(bool)
		return ok
	case traitTypeString:
		s, ok := v.(string)
		return ok && (t.MaxLength <= 0 || utf8.RuneCountInString(s) <= t.MaxLength)
	case traitTypeChoice:
		s, ok := v.(string)
		return ok && (s == "" || admits(s))
	case traitTypeList:
		list, ok := v.([]any)
		if !ok {
			return false
		}
		for _, item := range list {
			s, ok := item.(string)
			if !ok || !admits(s) {
				return false
			}
		}
		return true
	}
	return false
}

// traitValueEmpty is whether v gives a required trait nothing: absent, an
// empty string or an empty list. false is a value.
func traitValueEmpty(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return x == ""
	case []any:
		return len(x) == 0
	}
	return false
}

// GetStaffGroupsByIdTraits Get a group's providers
// (GET /api/v1/staff/groups/{id}/traits)
func (s *server) GetStaffGroupsByIdTraits(ctx context.Context, req gen.GetStaffGroupsByIdTraitsRequestObject) (gen.GetStaffGroupsByIdTraitsResponseObject, error) {
	q := store.New(s.deps.Pool)
	if _, err := q.GetGroup(ctx, req.Id); errors.Is(err, pgx.ErrNoRows) {
		return gen.GetStaffGroupsByIdTraits404Response{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("staff: read group %d: %w", req.Id, err)
	}
	rows, err := q.ListGroupTraits(ctx, req.Id)
	if err != nil {
		return nil, fmt.Errorf("staff: read group %d's sections: %w", req.Id, err)
	}
	out := gen.StaffGroupTraits{Providers: []gen.StaffGroupProvider{}}
	for _, p := range s.deps.IdentityProvisioners {
		if p == nil {
			continue
		}
		var row *store.StaffGroupTrait
		if i := slices.IndexFunc(rows, func(r store.StaffGroupTrait) bool { return r.Provider == p.Key() }); i >= 0 {
			row = &rows[i]
		}
		section, err := sectionResponse(s.askProvider(ctx, p), row)
		if err != nil {
			return nil, err
		}
		out.Providers = append(out.Providers, section)
	}
	return gen.GetStaffGroupsByIdTraits200JSONResponse(out), nil
}

// PutStaffGroupsByIdTraitsByProvider Set a group's provider
// (PUT /api/v1/staff/groups/{id}/traits/{provider})
//
// enabled true checks the values against the provider's declaration and
// turns the section on, pending and due now, so the worker sends them at its
// next cycle; a provider that cannot declare cannot have values checked, and
// the request fails. enabled false turns an existing section off — its
// values kept, the worker retiring the group as the settings say — and on
// no section writes nothing.
func (s *server) PutStaffGroupsByIdTraitsByProvider(ctx context.Context, req gen.PutStaffGroupsByIdTraitsByProviderRequestObject) (gen.PutStaffGroupsByIdTraitsByProviderResponseObject, error) {
	var body gen.StaffGroupTraitsRequest
	if req.Body != nil {
		body = *req.Body
	}
	p, ok := s.provisioner(req.Provider)
	if !ok {
		return gen.PutStaffGroupsByIdTraitsByProvider404Response{}, nil
	}
	q := store.New(s.deps.Pool)
	if _, err := q.GetGroup(ctx, req.Id); errors.Is(err, pgx.ErrNoRows) {
		return gen.PutStaffGroupsByIdTraitsByProvider404Response{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("staff: read group %d: %w", req.Id, err)
	}
	section := s.askProvider(ctx, p)
	var stored []byte
	if body.Enabled {
		if !section.ok {
			// The values cannot be checked: nothing is written.
			return gen.PutStaffGroupsByIdTraitsByProvider503ApplicationProblemPlusJSONResponse(groupProviderUnavailable()), nil
		}
		var values map[string]any
		if body.Values != nil {
			values = *body.Values
		}
		checked, f := checkTraitValues(section.traits, values, true)
		if f.failed() {
			return gen.PutStaffGroupsByIdTraitsByProvider400ApplicationProblemPlusJSONResponse(invalid(invalidGroupTraitsTitle, f)), nil
		}
		var err error
		if stored, err = json.Marshal(checked); err != nil {
			return nil, fmt.Errorf("staff: encode group %d's values under %s: %w", req.Id, req.Provider, err)
		}
	}
	var row *store.StaffGroupTrait
	err := s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		g, err := lockWritableGroup(ctx, txq, req.Id)
		if err != nil {
			return err
		}
		if body.Enabled {
			saved, err := txq.UpsertGroupTrait(ctx, store.UpsertGroupTraitParams{
				GroupID: g.ID, Provider: p.Key(), Values: stored, Now: s.deps.Clock(),
			})
			if err != nil {
				return fmt.Errorf("staff: turn %s on for group %d: %w", p.Key(), g.ID, err)
			}
			row = &saved
			return nil
		}
		saved, err := txq.DisableGroupTrait(ctx, store.DisableGroupTraitParams{GroupID: g.ID, Provider: p.Key(), Now: s.deps.Clock()})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		} else if err != nil {
			return fmt.Errorf("staff: turn %s off for group %d: %w", p.Key(), g.ID, err)
		}
		row = &saved
		return nil
	})
	if r, ok := asRefusal(err); ok {
		if r.status == http.StatusNotFound {
			return gen.PutStaffGroupsByIdTraitsByProvider404Response{}, nil
		}
		return gen.PutStaffGroupsByIdTraitsByProvider409ApplicationProblemPlusJSONResponse(r.problem), nil
	}
	if err != nil {
		return nil, fmt.Errorf("staff: set group %d's section under %s: %w", req.Id, req.Provider, err)
	}
	out, err := sectionResponse(section, row)
	if err != nil {
		return nil, err
	}
	return gen.PutStaffGroupsByIdTraitsByProvider200JSONResponse(out), nil
}
