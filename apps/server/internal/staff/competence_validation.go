package staff

import (
	"fmt"
	"net/http"
	"regexp"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is competence's validation (staff competence design S1–S6, §3,
// §4): the catalogue's keys, names and validity, and a person's row's level,
// dates and texts. The router does not judge a request's enums or bounds, so
// these rules are the module's, reported per field.

// The catalogue's bounds (§3.1, §3.2; the plan's exact values).
var (
	competenceCategoryKeyPattern = regexp.MustCompile(`^[a-z0-9-]{1,40}$`)
	competenceKeyPattern         = regexp.MustCompile(`^[a-z0-9-]{1,60}$`)
)

const (
	competenceNameMaxLength   = 100
	competenceIssuerMaxLength = 200
	competenceReferenceMax    = 100
	competenceNoteMaxLength   = 200
	validityMonthsMin         = 1
	validityMonthsMax         = 240
	reminderLeadDaysMax       = 365
	competenceLevelMax        = 4
)

// A competence's kind (S1): a skill — a proficiency, a level when it has
// one, never a validity — or a certificate.
const (
	kindSkill       = "skill"
	kindCertificate = "certificate"
)

var competenceKinds = []string{kindSkill, kindCertificate}

// Who entered a row (S3): the person, a manager of theirs or staff:approve,
// or HR.
const (
	sourceSelf    = "self"
	sourceManager = "manager"
	sourceHR      = "hr"
)

// A verification's basis (S4): the verifier saw the evidence, or the work.
const (
	basisEvidence = "evidence"
	basisObserved = "observed"
)

var verificationBases = []string{basisEvidence, basisObserved}

// defaultLeadDays is the reminder lead a validity gets when none is given
// (S7, reading 2): 30 days for a validity of up to a year, 60 up to two
// years, 180 beyond.
func defaultLeadDays(validityMonths int16) int16 {
	switch {
	case validityMonths <= 12:
		return 30
	case validityMonths <= 24:
		return 60
	}
	return 180
}

// parseCompetenceNames is a competence's or a category's two names, each 1
// to 100 characters.
func parseCompetenceNames(names gen.StaffCompetenceNames, f *fieldErrors) (string, string) {
	en, msg := requiredText("An English name", names.En, competenceNameMaxLength)
	f.add("names.en", msg)
	nb, msg := requiredText("A Norwegian name", names.Nb, competenceNameMaxLength)
	f.add("names.nb", msg)
	return en, nb
}

// competenceSpec is what a competence's create and change share beyond its
// names: the level, the validity, the lead and the issuer hint.
type competenceSpec struct {
	HasLevel         bool
	ValidityMonths   *int16
	ReminderLeadDays *int16
	IssuerHint       *string
}

// parseCompetenceSpec judges a competence of kind: a validity of 1 to 240
// months only on a certificate, a lead of 0 to 365 days only with a validity
// — the default lead by S7's rule when a validity has none — and an issuer
// hint of at most 200 characters.
func parseCompetenceSpec(kind string, hasLevel bool, validity, lead *int32, issuerHint *string, f *fieldErrors) competenceSpec {
	out := competenceSpec{HasLevel: hasLevel}
	if validity != nil {
		switch {
		case kind == kindSkill:
			f.add("validityMonths", "A skill has no validity; only a certificate is valid for a number of months")
		case *validity < validityMonthsMin || *validity > validityMonthsMax:
			f.add("validityMonths", fmt.Sprintf("A validity is %d to %d months", validityMonthsMin, validityMonthsMax))
		default:
			v := int16(*validity)
			out.ValidityMonths = &v
		}
	}
	if lead != nil {
		switch {
		case validity == nil:
			f.add("reminderLeadDays", "A reminder lead is given only with a validity")
		case *lead < 0 || *lead > reminderLeadDaysMax:
			f.add("reminderLeadDays", fmt.Sprintf("A reminder lead is 0 to %d days", reminderLeadDaysMax))
		default:
			v := int16(*lead)
			out.ReminderLeadDays = &v
		}
	} else if out.ValidityMonths != nil {
		v := defaultLeadDays(*out.ValidityMonths)
		out.ReminderLeadDays = &v
	}
	var msg string
	out.IssuerHint, msg = optionalText("An issuer hint", issuerHint, competenceIssuerMaxLength)
	f.add("issuerHint", msg)
	return out
}

// parseLevel is a row's level judged against its competence (S2): required
// on a competence with a level (level_required), refused on one without
// (level_not_allowed), 0 to 4.
func parseLevel(level *int32, hasLevel bool, f *fieldErrors) *int16 {
	switch {
	case hasLevel && level == nil:
		f.addCoded("level", codeLevelRequired, "This competence takes a level of 0 to 4")
	case !hasLevel && level != nil:
		f.addCoded("level", codeLevelNotAllowed, "This competence takes no level")
	case level != nil && (*level < 0 || *level > competenceLevelMax):
		f.add("level", fmt.Sprintf("A level is 0 to %d", competenceLevelMax))
	case level != nil:
		v := int16(*level)
		return &v
	}
	return nil
}

// competenceRowFields is a person's row's dates and texts as a write gives them.
type competenceRowFields struct {
	IssuedOn  *time.Time
	ValidFrom time.Time
	ValidTo   *time.Time
	Issuer    *string
	Reference *string
}

// dayOf is a request's date as the calendar day it names, nil when absent.
func dayOf(d *openapi_types.Date) *time.Time {
	if d == nil {
		return nil
	}
	day := civilDate(d).Time
	return &day
}

// parseRowFields judges a row's dates and texts (S3): validFrom left out is
// fallback when fallback is set — the row's own on a change — else the issue
// date, else today; validTo on or after validFrom; the issuer at most 200
// characters, the reference at most 100.
func parseRowFields(issuedOn, validFrom, validTo *openapi_types.Date, issuer, reference *string, fallback *time.Time, today time.Time, f *fieldErrors) competenceRowFields {
	out := competenceRowFields{IssuedOn: dayOf(issuedOn), ValidTo: dayOf(validTo)}
	switch {
	case validFrom != nil:
		out.ValidFrom = *dayOf(validFrom)
	case fallback != nil:
		out.ValidFrom = *fallback
	case out.IssuedOn != nil:
		out.ValidFrom = *out.IssuedOn
	default:
		out.ValidFrom = today
	}
	if out.ValidTo != nil && out.ValidTo.Before(out.ValidFrom) {
		f.add("validTo", fmt.Sprintf("The end of the validity is on or after its start, %s", out.ValidFrom.Format(time.DateOnly)))
	}
	var msg string
	out.Issuer, msg = optionalText("An issuer", issuer, competenceIssuerMaxLength)
	f.add("issuer", msg)
	out.Reference, msg = optionalText("A reference", reference, competenceReferenceMax)
	f.add("reference", msg)
	return out
}

// competenceAvailable is whether a new row may name c (§4 "Hidden and
// archived"): neither hidden nor archived. An unknown one is unavailable too.
func competenceAvailable(c store.StaffCompetence) bool {
	return !c.Hidden && c.ArchivedAt == nil
}

// unavailableCompetence is the 400 on competenceId for a competence a new
// row may not name.
func unavailableCompetence(id int64) error {
	var f fieldErrors
	f.addCoded("competenceId", codeCompetenceUnavailable,
		fmt.Sprintf("Competence %d is unknown, hidden or archived, and no new row names it", id))
	return refuseInvalid(invalidPersonCompetenceTitle, f)
}

// competenceRole is who the caller is to a person's competence rows (§4 "Who
// writes a competence row"): the person themselves — whatever else they
// hold, so they never verify their own — a verifier (a manager of theirs
// today, staff:approve) or HR (staff:manage), or nobody who writes.
type competenceRole int

const (
	roleNone competenceRole = iota
	roleSelf
	roleVerifier
	roleHR
)

// competenceRoleOf is a's role on the person's rows.
func competenceRoleOf(a audience) competenceRole {
	switch {
	case a.Self:
		return roleSelf
	case a.Manage:
		return roleHR
	case a.Manager || a.Approver:
		return roleVerifier
	}
	return roleNone
}

// source is what a row a role enters says of who entered it.
func (r competenceRole) source() string {
	switch r {
	case roleHR:
		return sourceHR
	case roleVerifier:
		return sourceManager
	}
	return sourceSelf
}

// verifies is whether the role's rows are verified as it enters them
// (reading 5): a verifier's and HR's, never the person's.
func (r competenceRole) verifies() bool { return r == roleVerifier || r == roleHR }

// isVerified is whether a row is verified (S3): declared until stamped.
func isVerified(r store.StaffPersonCompetence) bool { return r.VerifiedAt != nil }

// isSuperseded is whether a row was renewed (S5): valid on no day, written
// by nobody.
func isSuperseded(r store.StaffPersonCompetence) bool { return r.SupersededByID != nil }

// supersededRow is the 409 a write of a superseded row answers.
func supersededRow() error {
	return refuseConflict(conflict(codeSuperseded, "Superseded",
		"This row was renewed by a newer one and is kept as history. Change the current row instead."))
}

// verifiedRow is the 409 the person's change or removal of their own
// verified row answers (§4): theirs to read and renew.
func verifiedRow() error {
	return refuseConflict(conflict(codeVerifiedRow, "Verified",
		"This competence has been verified, so it is no longer yours to change or remove. Renew it, or ask your manager or HR."))
}

// competenceHeld is the 409 a second current row of one competence answers
// (S3).
func competenceHeld() error {
	return refuseConflict(conflict(codeCompetenceHeld, "Competence held",
		"The person already holds this competence. Change or renew that row instead."))
}

// forbidden is the uniform 403, raised from inside a transaction: its
// problem is empty, and the handler answers the access layer's own body.
func forbidden() error { return &refusal{status: http.StatusForbidden} }

// ownRecord is the 403 to the person verifying their own row (S4).
func ownRecord() error {
	return refuseForbidden(codeOwnRecord, "Your own competence",
		"You cannot verify your own competence. Your manager, staff:approve or HR verifies it.")
}
