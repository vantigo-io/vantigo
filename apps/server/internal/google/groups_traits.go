package google

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/google/store"
)

// This file is the traits a group may carry for Google Workspace (Google
// Workspace design G20): Staff renders its form from GroupTraits alone, and
// EnsureGroup (groups.go) validates the values against the same rules.

// The traits' keys.
const (
	traitEmail      = "email"
	traitVisibility = "visibility"
	traitLicences   = "licences"
)

// The visibility trait's choices.
const (
	visibilityDomain  = "domain"
	visibilityMembers = "members"
)

// The bounds a group's value reaching Google is held to.
const (
	maxGroupName        = 75
	maxGroupDescription = 4096
)

// GroupTraits declares the three traits a group may carry for Google
// Workspace (G20): its address, fixed once the group exists and made from
// the group's key at the address domain when left empty; who sees the group
// and its members; and the licences its members get, whose choices are the
// organisation's licences as the last read of them cached them on the
// connection's row. It reads that row alone, never the network.
func (p *provisioner) GroupTraits(ctx context.Context) ([]contracts.GroupTrait, error) {
	if p.deps.Pool == nil {
		return nil, errors.New("google: no database to read the licences from")
	}
	conn, err := store.New(p.deps.Pool).GetConnection(ctx)
	if err != nil {
		return nil, fmt.Errorf("google: read the connection: %w", err)
	}
	skus, err := cachedSkus(conn)
	if err != nil {
		return nil, err
	}
	licences := make([]contracts.TraitChoice, 0, len(skus))
	for _, s := range skus {
		n := strconv.Itoa(int(s.Assigned))
		licences = append(licences, contracts.TraitChoice{Value: s.ProductID + "/" + s.SkuID, Label: map[string]string{
			"en": s.Name + " (" + n + " assigned)",
			"nb": s.Name + " (" + n + " tildelt)",
		}})
	}
	return []contracts.GroupTrait{
		{
			Key:   traitEmail,
			Type:  "string",
			Label: map[string]string{"en": "Group address", "nb": "Gruppeadresse"},
			Description: map[string]string{
				"en": "The group's email address in Google Workspace, on one of the organisation's verified domains; the group's key at the account rules' domain when left empty. It cannot change once the group exists.",
				"nb": "Gruppens e-postadresse i Google Workspace, på et av organisasjonens bekreftede domener; gruppens nøkkel på domenet i kontoreglene når feltet er tomt. Den kan ikke endres når gruppen finnes.",
			},
			DocURL:    "https://support.google.com/a/answer/9400082",
			MaxLength: maxEmail,
		},
		{
			Key:   traitVisibility,
			Type:  "choice",
			Label: map[string]string{"en": "Who sees the group", "nb": "Hvem ser gruppen"},
			Description: map[string]string{
				"en": "Anyone in the organisation sees the group, its members and finds it in the directory; or only its members do.",
				"nb": "Alle i organisasjonen ser gruppen og medlemmene og finner den i katalogen; eller bare medlemmene gjør det.",
			},
			Choices: []contracts.TraitChoice{
				{Value: visibilityDomain, Label: map[string]string{"en": "Everyone in the organisation", "nb": "Alle i organisasjonen"}},
				{Value: visibilityMembers, Label: map[string]string{"en": "Members only", "nb": "Bare medlemmene"}},
			},
			DocURL:  "https://support.google.com/a/answer/167097",
			Default: visibilityDomain,
		},
		{
			Key:   traitLicences,
			Type:  "list",
			Label: map[string]string{"en": "Licences", "nb": "Lisenser"},
			Description: map[string]string{
				"en": "The licences every member with an account in Google Workspace is assigned, as long as they are in the group. The choices are the organisation's licences as the last read of them on the Accounts tab found them.",
				"nb": "Lisensene hvert medlem med konto i Google Workspace tildeles, så lenge de er i gruppen. Valgene er organisasjonens lisenser slik den siste lesingen av dem på Kontoer-fanen fant dem.",
			},
			Choices: licences,
			DocURL:  "https://support.google.com/a/answer/1041297",
		},
	}, nil
}

// groupValues are a GroupSpec's trait values as EnsureGroup writes them: the
// address (lower-case, "" when the trait is empty), the visibility, and the
// licences (Staff's; read here only to be checked).
type groupValues struct {
	email      string
	visibility string
	licences   []string
}

// traitError is the refusal of field's value with code.
func traitError(field, code string) error {
	return &contracts.GroupTraitError{Field: field, Code: code}
}

// parseGroupValues checks values against the traits GroupTraits declares —
// the address an address on one of domains, the visibility one of its
// choices, the licences a list of productId/skuId — and answers them, or
// the first refusal as a *contracts.GroupTraitError. A key no trait declares
// is ignored.
func parseGroupValues(values map[string]any, domains []string) (groupValues, error) {
	out := groupValues{visibility: visibilityDomain}
	text := func(key string, maxLen int) (string, error) {
		raw, ok := values[key]
		if !ok || raw == nil {
			return "", nil
		}
		s, ok := raw.(string)
		if !ok || utf8.RuneCountInString(s) > maxLen {
			return "", traitError(key, contracts.TraitInvalid)
		}
		return strings.TrimSpace(s), nil
	}
	email, err := text(traitEmail, maxEmail)
	if err != nil {
		return out, err
	}
	if email != "" {
		email = strings.ToLower(email)
		_, domain, ok := strings.Cut(email, "@")
		if !addressPattern.MatchString(email) || !ok || !slices.Contains(domains, domain) {
			return out, traitError(traitEmail, contracts.TraitInvalid)
		}
		out.email = email
	}
	visibility, err := text(traitVisibility, 20)
	if err != nil {
		return out, err
	}
	switch v := strings.ToLower(visibility); v {
	case "":
	case visibilityDomain, visibilityMembers:
		out.visibility = v
	default:
		return out, traitError(traitVisibility, contracts.TraitInvalid)
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
			if _, valid := parseLicence(s); !ok || !valid {
				return out, traitError(traitLicences, contracts.TraitInvalid)
			}
			out.licences = append(out.licences, s)
		}
	}
	return out, nil
}

// groupLocal is a Staff group's key as an address's local part: folded to
// the mailbox alphabet's letters, digits and hyphens, "" when nothing is
// left.
func groupLocal(key string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(key) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '.' || r == '_' {
			b.WriteRune(r)
		}
	}
	return strings.Trim(b.String(), ".")
}
