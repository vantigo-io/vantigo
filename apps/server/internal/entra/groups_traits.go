package entra

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/entra/store"
)

// This file is the traits a group may carry for Entra (Entra design D10,
// plan task 4) and the tenant's licences cached on the connection's row
// they are declared from: Staff renders its form from GroupTraits alone,
// and EnsureGroup (groups.go) validates the values against the same rules.

// The traits' keys.
const (
	traitType         = "type"
	traitMailNickname = "mailNickname"
	traitVisibility   = "visibility"
	traitDescription  = "description"
	traitTeam         = "team"
	traitLicences     = "licences"
)

// The group types, as the type trait's choices and entra.groups.type name
// them.
const (
	groupTypeSecurity     = "security"
	groupTypeMicrosoft365 = "microsoft365"
)

// The visibility trait's choices, and Graph's spelling of them.
const (
	visibilityPrivate = "private"
	visibilityPublic  = "public"
)

// The bounds a value reaching Graph is held to (research §3.3).
const (
	maxGroupDisplayName = 256
	maxGroupDescription = 1024
	maxMailNickname     = 64
	// maxCachedSkus is how many of the tenant's licences the connection's
	// row keeps.
	maxCachedSkus = 200
)

// cachedSku is one licence as connection.skus keeps it.
type cachedSku struct {
	SkuID         string `json:"skuId"`
	SkuPartNumber string `json:"skuPartNumber"`
	Enabled       int32  `json:"enabled"`
	Consumed      int32  `json:"consumed"`
}

// cacheSkus writes the tenant's licences to the connection's row, by part
// number, at most maxCachedSkus of them: what GroupTraits declares the
// licences trait from with no network (plan reading 1).
func cacheSkus(ctx context.Context, q *store.Queries, skus []cachedSku) error {
	out := slices.Clone(skus)
	slices.SortFunc(out, func(a, b cachedSku) int {
		return cmp.Or(strings.Compare(a.SkuPartNumber, b.SkuPartNumber), strings.Compare(a.SkuID, b.SkuID))
	})
	if len(out) > maxCachedSkus {
		out = out[:maxCachedSkus]
	}
	if out == nil {
		out = []cachedSku{}
	}
	data, err := json.Marshal(out)
	if err != nil {
		return fmt.Errorf("entra: encode the licences: %w", err)
	}
	if err := q.SetSkus(ctx, data); err != nil {
		return fmt.Errorf("entra: cache the licences: %w", err)
	}
	return nil
}

// cachedSkus reads the licences connection.skus keeps; an empty column is
// none.
func cachedSkus(conn store.EntraConnection) ([]cachedSku, error) {
	var skus []cachedSku
	if len(conn.Skus) == 0 {
		return skus, nil
	}
	if err := json.Unmarshal(conn.Skus, &skus); err != nil {
		return nil, fmt.Errorf("entra: read the cached licences: %w", err)
	}
	return skus, nil
}

// GroupTraits declares the six traits a group may carry for Entra (design
// D10): its type, a required choice it is made with and never changes; its
// mail nickname, fixed once the group exists; its visibility, its team and
// — a Microsoft 365 group's alone; its description; and the licences its
// members get, whose choices are the tenant's licences as the last
// connection test or licences read cached them on the connection's row,
// each labelled with the seats left. It reads that row alone, never the
// network: Staff asks it whenever a group's page is read.
func (p *provisioner) GroupTraits(ctx context.Context) ([]contracts.GroupTrait, error) {
	if p.deps.Pool == nil {
		return nil, errors.New("entra: no database to read the licences from")
	}
	conn, err := store.New(p.deps.Pool).GetConnection(ctx)
	if err != nil {
		return nil, fmt.Errorf("entra: read the connection: %w", err)
	}
	skus, err := cachedSkus(conn)
	if err != nil {
		return nil, err
	}
	licences := make([]contracts.TraitChoice, 0, len(skus))
	for _, s := range skus {
		if s.SkuPartNumber == "" {
			continue
		}
		left := strconv.Itoa(int(max(s.Enabled-s.Consumed, 0)))
		of := strconv.Itoa(int(s.Enabled))
		licences = append(licences, contracts.TraitChoice{Value: s.SkuPartNumber, Label: map[string]string{
			"en": s.SkuPartNumber + " (" + left + " of " + of + " available)",
			"nb": s.SkuPartNumber + " (" + left + " av " + of + " ledige)",
		}})
	}
	return []contracts.GroupTrait{
		{
			Key:   traitType,
			Type:  "choice",
			Label: map[string]string{"en": "Group type", "nb": "Gruppetype"},
			Description: map[string]string{
				"en": "A security group grants access to apps and resources; a Microsoft 365 group adds a shared mailbox, calendar and files, and can become a team. It cannot change once the group exists.",
				"nb": "En sikkerhetsgruppe gir tilgang til apper og ressurser; en Microsoft 365-gruppe gir i tillegg felles postboks, kalender og filer, og kan bli et team. Den kan ikke endres når gruppen finnes.",
			},
			Choices: []contracts.TraitChoice{
				{Value: groupTypeSecurity, Label: map[string]string{"en": "Security", "nb": "Sikkerhet"}},
				{Value: groupTypeMicrosoft365, Label: map[string]string{"en": "Microsoft 365", "nb": "Microsoft 365"}},
			},
			DocURL:   "https://learn.microsoft.com/en-us/entra/fundamentals/concept-learn-about-groups",
			Default:  groupTypeSecurity,
			Required: true,
		},
		{
			Key:   traitMailNickname,
			Type:  "string",
			Label: map[string]string{"en": "Mail nickname", "nb": "E-postalias"},
			Description: map[string]string{
				"en": "The group's alias in the tenant, at most 64 characters of a–z, 0–9 and hyphens; made from the group's key when left empty. It cannot change once the group exists.",
				"nb": "Gruppens alias i tenanten, høyst 64 tegn av a–z, 0–9 og bindestrek; lages av gruppens nøkkel når feltet er tomt. Det kan ikke endres når gruppen finnes.",
			},
			DocURL:    "https://learn.microsoft.com/en-us/graph/api/resources/group?view=graph-rest-1.0",
			MaxLength: maxMailNickname,
		},
		{
			Key:   traitVisibility,
			Type:  "choice",
			Label: map[string]string{"en": "Visibility", "nb": "Synlighet"},
			Description: map[string]string{
				"en": "Who can see a Microsoft 365 group's content and join it: its members alone, or everyone in the organisation. Not for a security group.",
				"nb": "Hvem som kan se innholdet i en Microsoft 365-gruppe og bli med: bare medlemmene, eller alle i organisasjonen. Ikke for en sikkerhetsgruppe.",
			},
			Choices: []contracts.TraitChoice{
				{Value: visibilityPrivate, Label: map[string]string{"en": "Private", "nb": "Privat"}},
				{Value: visibilityPublic, Label: map[string]string{"en": "Public", "nb": "Offentlig"}},
			},
			DocURL: "https://learn.microsoft.com/en-us/graph/api/resources/group?view=graph-rest-1.0",
		},
		{
			Key:   traitDescription,
			Type:  "string",
			Label: map[string]string{"en": "Description in the tenant", "nb": "Beskrivelse i tenanten"},
			Description: map[string]string{
				"en": "The group's description in Microsoft Entra ID, at most 1024 characters; the group's own description when left empty.",
				"nb": "Gruppens beskrivelse i Microsoft Entra ID, høyst 1024 tegn; gruppens egen beskrivelse når feltet er tomt.",
			},
			DocURL:    "https://learn.microsoft.com/en-us/graph/api/group-post-groups?view=graph-rest-1.0",
			MaxLength: maxGroupDescription,
		},
		{
			Key:   traitTeam,
			Type:  "bool",
			Label: map[string]string{"en": "Team in Microsoft Teams", "nb": "Team i Microsoft Teams"},
			Description: map[string]string{
				"en": "Makes a Microsoft 365 group a team, once it is fifteen minutes old and has a person as its owner. Turning it off later leaves the team.",
				"nb": "Gjør en Microsoft 365-gruppe til et team når den er femten minutter gammel og har en person som eier. Å slå det av senere lar teamet bli.",
			},
			DocURL:  "https://learn.microsoft.com/en-us/graph/api/team-put-teams?view=graph-rest-1.0",
			Default: false,
		},
		{
			Key:   traitLicences,
			Type:  "list",
			Label: map[string]string{"en": "Licences", "nb": "Lisenser"},
			Description: map[string]string{
				"en": "The licences every member with an account in the tenant is assigned directly, as long as they are in the group. The choices are the tenant's licences as the last connection test read them.",
				"nb": "Lisensene hvert medlem med konto i tenanten tildeles direkte, så lenge de er i gruppen. Valgene er tenantens lisenser slik den siste tilkoblingstesten leste dem.",
			},
			Choices: licences,
			DocURL:  "https://learn.microsoft.com/en-us/graph/api/user-assignlicense?view=graph-rest-1.0",
		},
	}, nil
}

// groupValues are a GroupSpec's trait values as EnsureGroup writes them:
// the type, the nickname folded (empty when the trait is), the visibility
// (empty when none), the trait's description, whether a team is wanted,
// and the licences (Staff's; read here only to be checked).
type groupValues struct {
	kind        string
	nickname    string
	visibility  string
	description string
	team        bool
	licences    []string
}

// traitError is the refusal of field's value with code.
func traitError(field, code string) error {
	return &contracts.GroupTraitError{Field: field, Code: code}
}

// parseGroupValues checks values against the traits GroupTraits declares —
// the type required and one of its choices, a string within its length, a
// visibility or a team on a Microsoft 365 group alone, the licences a list
// of part numbers — and answers them, or the first refusal as a
// *contracts.GroupTraitError. A key no trait declares is ignored.
func parseGroupValues(values map[string]any) (groupValues, error) {
	var out groupValues
	text := func(key string, maxLen int) (string, error) {
		raw, ok := values[key]
		if !ok || raw == nil {
			return "", nil
		}
		s, ok := raw.(string)
		if !ok || len([]rune(s)) > maxLen {
			return "", traitError(key, contracts.TraitInvalid)
		}
		return strings.TrimSpace(s), nil
	}
	kind, err := text(traitType, 20)
	if err != nil {
		return out, err
	}
	switch kind {
	case "":
		return out, traitError(traitType, contracts.TraitRequired)
	case groupTypeSecurity, groupTypeMicrosoft365:
		out.kind = kind
	default:
		return out, traitError(traitType, contracts.TraitInvalid)
	}
	nickname, err := text(traitMailNickname, maxMailNickname)
	if err != nil {
		return out, err
	}
	if nickname != "" {
		if out.nickname = mailNicknameOf(nickname); out.nickname == "" {
			return out, traitError(traitMailNickname, contracts.TraitInvalid)
		}
	}
	visibility, err := text(traitVisibility, 20)
	if err != nil {
		return out, err
	}
	switch v := strings.ToLower(visibility); {
	case v == "":
	case v != visibilityPrivate && v != visibilityPublic, out.kind != groupTypeMicrosoft365:
		return out, traitError(traitVisibility, contracts.TraitInvalid)
	default:
		out.visibility = v
	}
	if out.description, err = text(traitDescription, maxGroupDescription); err != nil {
		return out, err
	}
	if raw, ok := values[traitTeam]; ok && raw != nil {
		team, ok := raw.(bool)
		if !ok || (team && out.kind != groupTypeMicrosoft365) {
			return out, traitError(traitTeam, contracts.TraitInvalid)
		}
		out.team = team
	}
	if raw, ok := values[traitLicences]; ok && raw != nil {
		var list []any
		switch v := raw.(type) {
		case []any:
			list = v
		case []string:
			for _, s := range v {
				list = append(list, s)
			}
		default:
			return out, traitError(traitLicences, contracts.TraitInvalid)
		}
		for _, item := range list {
			s, ok := item.(string)
			if !ok || !codeSuffix.MatchString(s) {
				return out, traitError(traitLicences, contracts.TraitInvalid)
			}
			out.licences = append(out.licences, s)
		}
	}
	return out, nil
}

// mailNicknameOf is s as a mail nickname: folded (fold) and lower-cased,
// a–z, 0–9 and the hyphen kept, every run of other characters one hyphen
// between the kept ones, cut at 64. A Staff key is one already, kept as it
// is (the plan's "Exact values"); "" when nothing is left.
func mailNicknameOf(s string) string {
	var b strings.Builder
	gap := false
	for _, r := range strings.ToLower(fold(s)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			if gap && b.Len() > 0 {
				b.WriteByte('-')
			}
			gap = false
			b.WriteRune(r)
			continue
		}
		gap = true
	}
	out := b.String()
	if len(out) > maxMailNickname {
		out = out[:maxMailNickname]
	}
	return out
}
