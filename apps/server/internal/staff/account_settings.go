package staff

import (
	"fmt"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
)

// This file is the settings' two account fields (design §3.6, delivery C):
// createBeforeDays, how many days before the account's effective date the
// accounts worker prepares it, disabled (S22), and deleteExternalAfterDays,
// when an external directory's account is deleted after the last employment
// ended — inert until the Entra module reads it. Both are 00042's columns.
// Each keeps its value when a replace leaves it out (plan M8), as the
// retention periods do: a settings form that predates them, or edits other
// fields, never resets them.

// The default account kind (Entra design D5, 00060's
// settings.default_account_kind) is the kind a new person takes (InsertPerson
// reads it); people already registered keep theirs. Like the two above, it
// is kept when a replace leaves it out.

// The account settings' bounds (plan "Exact values").
const (
	createBeforeDaysMax        = 90
	deleteExternalAfterDaysMin = 1
	deleteExternalAfterDaysMax = 3650
)

// parsedAccountSettings is a validated replace of the two fields. A field
// left out is kept: createBeforeDays nil, deleteExternal false — whose
// deleteExternalAfterDays nil is otherwise a value, "never".
type parsedAccountSettings struct {
	createBeforeDays        *int16
	deleteExternal          bool
	deleteExternalAfterDays *int16
}

// parseAccountSettings judges the two fields of body into f. hasDeleteExternal
// is whether the body names deleteExternalAfterDays at all: the decoder reads
// a null and an absent property alike, and only the first clears it.
func parseAccountSettings(body gen.StaffSettingsRequest, hasDeleteExternal bool, f *fieldErrors) parsedAccountSettings {
	var out parsedAccountSettings
	if v := body.CreateBeforeDays; v != nil {
		if *v < 0 || *v > createBeforeDaysMax {
			f.add("createBeforeDays", fmt.Sprintf("An account is prepared 0 to %d days before it is enabled", createBeforeDaysMax))
		} else {
			days := int16(*v)
			out.createBeforeDays = &days
		}
	}
	if hasDeleteExternal {
		out.deleteExternal = true
		if v := body.DeleteExternalAfterDays; v != nil {
			if *v < deleteExternalAfterDaysMin || *v > deleteExternalAfterDaysMax {
				f.add("deleteExternalAfterDays", fmt.Sprintf("An external account is deleted %d to %d days after the last employment ended, or never (null)",
					deleteExternalAfterDaysMin, deleteExternalAfterDaysMax))
			} else {
				days := int16(*v)
				out.deleteExternalAfterDays = &days
			}
		}
	}
	return out
}

// accountKindOffered reports whether kind may be chosen for a person or as
// the default: local and external always, any other kind only when an
// enabled module offers the provider keyed by it (the IdentityProvisioner
// slot) — entra, in delivery B, the one other kind the column allows.
func (s *server) accountKindOffered(kind string) bool {
	switch kind {
	case contracts.AccountKindLocal, contracts.AccountKindExternal:
		return true
	}
	if !contracts.ValidAccountKind(kind) {
		return false
	}
	_, ok := s.provisioner(kind)
	return ok
}

// providerMissing records on f that kind, named on field, is no kind an
// enabled module offers: the coded rule provider_missing.
func providerMissing(f *fieldErrors, field, kind string) {
	f.addCoded(field, codeProviderMissing, fmt.Sprintf(
		"'%s' is no account kind this installation offers: local, external, or a provider an enabled module offers", kind))
}

// parseDefaultAccountKind judges the default account kind of a replace into
// f, nil when left out, which keeps it.
func (s *server) parseDefaultAccountKind(body gen.StaffSettingsRequest, f *fieldErrors) *string {
	if body.DefaultAccountKind == nil {
		return nil
	}
	kind := string(*body.DefaultAccountKind)
	if !s.accountKindOffered(kind) {
		providerMissing(f, "defaultAccountKind", kind)
		return nil
	}
	return &kind
}
