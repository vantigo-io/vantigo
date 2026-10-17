package staff

import (
	"fmt"

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
