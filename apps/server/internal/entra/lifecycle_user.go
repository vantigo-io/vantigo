package entra

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/entra/store"
)

// This file is the user as the lifecycle reads and writes it (Entra design
// D13, research §3.1): the properties read, Staff's spec mapped to Graph's
// names, the difference an update writes, the manager, the instants of the
// hire and the leave, and the codes a call reports.

// findSelect is what the employee id's filter reads (D13).
const findSelect = "id,userPrincipalName,mail,accountEnabled,onPremisesSyncEnabled,employeeId"

// userSelect is what the lifecycle reads of one user: every property it
// writes or judges.
const userSelect = "id,userPrincipalName,mail,accountEnabled,onPremisesSyncEnabled,employeeId," +
	"displayName,givenName,surname,preferredLanguage,usageLocation,jobTitle,department,companyName," +
	"employeeType,employeeOrgData,officeLocation,streetAddress,postalCode,city,country,businessPhones," +
	"mobilePhone,employeeHireDate,employeeLeaveDateTime,assignedLicenses,licenseAssignmentStates"

// stringProperties are the user's string properties the lifecycle writes, by
// Graph's name, with the longest value Graph keeps; a longer one is cut.
var stringProperties = map[string]int{
	"displayName": 256, "givenName": 64, "surname": 64, "preferredLanguage": 10, "usageLocation": 2,
	"jobTitle": 128, "department": 64, "companyName": 64, "employeeType": 64, "officeLocation": 128,
	"streetAddress": 1024, "postalCode": 40, "city": 128, "country": 128, "mobilePhone": 64,
}

// fieldProperties are the spec's Fields (design D8) Graph names alike; the
// other three — costCenter and division in employeeOrgData, businessPhone in
// businessPhones — are mapped by hand. A field outside both is ignored.
var fieldProperties = []string{
	"jobTitle", "department", "companyName", "employeeType", "officeLocation",
	"streetAddress", "postalCode", "city", "country", "mobilePhone",
}

// The Fields mapped by hand, and the longest value of each.
const (
	fieldCostCenter    = "costCenter"
	fieldDivision      = "division"
	fieldBusinessPhone = "businessPhone"
	maxOrgValue        = 64
	maxPhone           = 64
)

// errNoSuchUser: the id is not a user the tenant holds. Staff reads Account
// before it decides, which answers nil and records user_gone.
var errNoSuchUser = errors.New("entra: the tenant holds no such user")

// graphUser is a user as Graph answers it, the string properties in strs
// ("" for null).
type graphUser struct {
	ID                      string         `json:"id"`
	UserPrincipalName       string         `json:"userPrincipalName"`
	Mail                    *string        `json:"mail"`
	AccountEnabled          bool           `json:"accountEnabled"`
	OnPremisesSyncEnabled   *bool          `json:"onPremisesSyncEnabled"`
	EmployeeHireDate        *time.Time     `json:"employeeHireDate"`
	EmployeeLeaveDateTime   *time.Time     `json:"employeeLeaveDateTime"`
	EmployeeOrgData         *graphOrgData  `json:"employeeOrgData"`
	BusinessPhones          []string       `json:"businessPhones"`
	AssignedLicenses        []graphSkuRef  `json:"assignedLicenses"`
	LicenseAssignmentStates []graphLicence `json:"licenseAssignmentStates"`
	strs                    map[string]string
}

// graphOrgData is employeeOrgData: both values written on every update, as
// one left out is cleared.
type graphOrgData struct {
	CostCenter *string `json:"costCenter"`
	Division   *string `json:"division"`
}

type graphSkuRef struct {
	SkuID string `json:"skuId"`
}

// graphLicence is one of licenseAssignmentStates: assignedByGroup null for a
// direct assignment, error "None" or the reason (CountViolation, …).
type graphLicence struct {
	SkuID           string  `json:"skuId"`
	AssignedByGroup *string `json:"assignedByGroup"`
	State           string  `json:"state"`
	Error           string  `json:"error"`
}

func (u *graphUser) UnmarshalJSON(data []byte) error {
	type plain graphUser
	var p plain
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	p.strs = map[string]string{}
	for key := range stringProperties {
		var v *string
		if r, ok := raw[key]; ok && json.Unmarshal(r, &v) == nil && v != nil {
			p.strs[key] = *v
		}
	}
	*u = graphUser(p)
	return nil
}

// synced reports whether u is synchronised from on-premises.
func (u graphUser) synced() bool { return u.OnPremisesSyncEnabled != nil && *u.OnPremisesSyncEnabled }

// objectID parses a user's id; ok false when it is not one.
func objectID(s string) (uuid.UUID, bool) {
	id, err := uuid.Parse(strings.TrimSpace(s))
	return id, err == nil && id != uuid.Nil
}

// query is a path's query, Graph's way: url.Values encoded, a space as %20.
func query(v url.Values) string {
	return strings.ReplaceAll(v.Encode(), "+", "%20")
}

// userPath is /users/{id}, and more after it.
func userPath(id uuid.UUID, rest ...string) string {
	return "/users/" + id.String() + strings.Join(rest, "")
}

// getUser reads the user with id; errNoSuchUser (wrapped) on a 404.
func (p *provisioner) getUser(ctx context.Context, s session, id uuid.UUID) (graphUser, error) {
	var u graphUser
	err := p.client.get(ctx, s.cred, userPath(id, "?", query(url.Values{"$select": {userSelect}})), &u)
	if isStatus(err, http.StatusNotFound) {
		return graphUser{}, fmt.Errorf("%w: %w", errNoSuchUser, err)
	}
	return u, err
}

// findByNumber lists the users whose employeeId is number: the filter with
// the quote doubled, as OData escapes it, and the value encoded.
func (p *provisioner) findByNumber(ctx context.Context, s session, number string) ([]graphUser, error) {
	filter := "employeeId eq '" + strings.ReplaceAll(number, "'", "''") + "'"
	var page struct {
		Value []graphUser `json:"value"`
	}
	err := p.client.get(ctx, s.cred, "/users?"+query(url.Values{"$filter": {filter}, "$select": {findSelect}}), &page)
	return page.Value, err
}

// ensureRow is the account's row, made from u when the module has none yet
// — a user Staff linked before this row existed, or one whose row was lost
// — as one the module found, not created, under number.
func (p *provisioner) ensureRow(ctx context.Context, s session, u graphUser, id uuid.UUID, number string) (store.EntraAccount, error) {
	row, err := s.q.GetAccount(ctx, id)
	if err == nil {
		return row, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return store.EntraAccount{}, fmt.Errorf("entra: read the account: %w", err)
	}
	state := stateDisabled
	if u.AccountEnabled {
		state = stateEnabled
	}
	if number == "" {
		// A call without a spec (Delete): nothing to key a row by, so none
		// is made; the event is written all the same.
		return store.EntraAccount{ObjectID: id, UserPrincipalName: u.UserPrincipalName, State: state, Reported: []string{}}, nil
	}
	row, err = s.q.UpsertAccount(ctx, store.UpsertAccountParams{
		ObjectID: id, EmployeeNumber: number, UserPrincipalName: u.UserPrincipalName, State: state,
		CreatedByUs: false, Reported: []string{}, Now: p.client.now(),
	})
	if err != nil {
		return store.EntraAccount{}, fmt.Errorf("entra: record the account: %w", err)
	}
	return row, nil
}

// attributes are what the spec asks of a user's properties: the string
// properties by Graph's name ("" clears one), businessPhones (nil leaves
// them), and the employeeOrgData values the spec names.
type attributes struct {
	strs   map[string]string
	phones []string
	org    map[string]string
}

// wantedAttributes maps spec to Graph's names (design D8), each value cut to
// what Graph keeps; usageLocation is the connection's choice (usageLocation).
func wantedAttributes(spec contracts.ExternalSpec, usage string) attributes {
	a := attributes{strs: map[string]string{}, org: map[string]string{}}
	set := func(key, v string) { a.strs[key] = cut(strings.TrimSpace(v), stringProperties[key]) }
	if d := displayName(spec); d != "" {
		set("displayName", d)
	}
	if g := strings.TrimSpace(spec.GivenName); g != "" {
		set("givenName", g)
	}
	if f := strings.TrimSpace(spec.FamilyName); f != "" {
		set("surname", f)
	}
	switch strings.ToLower(strings.TrimSpace(spec.PreferredLanguage)) {
	case "nb", "no", "nn", "nb-no":
		set("preferredLanguage", "nb-NO")
	case "en", "en-gb":
		set("preferredLanguage", "en-GB")
	}
	set("usageLocation", usage)
	for key, v := range spec.Fields {
		switch {
		case key == fieldCostCenter || key == fieldDivision:
			a.org[key] = cut(strings.TrimSpace(v), maxOrgValue)
		case key == fieldBusinessPhone:
			a.phones = []string{}
			if v = cut(strings.TrimSpace(v), maxPhone); v != "" {
				a.phones = []string{v}
			}
		case slices.Contains(fieldProperties, key):
			set(key, v)
		}
	}
	return a
}

// displayName is the spec's display name, else the given and family names.
func displayName(spec contracts.ExternalSpec) string {
	if d := strings.TrimSpace(spec.DisplayName); d != "" {
		return d
	}
	return strings.TrimSpace(strings.TrimSpace(spec.GivenName) + " " + strings.TrimSpace(spec.FamilyName))
}

// usageLocation is the spec's work country when it is an ISO 3166-1 code,
// else the connection's default.
func usageLocation(spec contracts.ExternalSpec, conn store.EntraConnection) string {
	if c := strings.ToUpper(strings.TrimSpace(spec.WorkCountry)); c != "" {
		if _, ok := iso3166Alpha2[c]; ok {
			return c
		}
	}
	return conn.DefaultUsageLocation
}

// cut is s at most n characters long.
func cut(s string, n int) string {
	if n <= 0 || utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// createBody adds the create's properties to body: the non-empty ones.
func (a attributes) createBody(body map[string]any) {
	for key, v := range a.strs {
		if v != "" {
			body[key] = v
		}
	}
	if len(a.phones) > 0 {
		body["businessPhones"] = a.phones
	}
	if a.org[fieldCostCenter] != "" || a.org[fieldDivision] != "" {
		body["employeeOrgData"] = orgBody(a.org[fieldCostCenter], a.org[fieldDivision])
	}
}

// orgBody is employeeOrgData whole, an empty value null.
func orgBody(costCenter, division string) map[string]any {
	return map[string]any{"costCenter": nullable(costCenter), "division": nullable(division)}
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// patch is what an update writes to u: each property whose value differs,
// by Graph's name, the names changed in order, and whether a sensitive one
// (a phone) is among them.
func (a attributes) patch(u graphUser) (map[string]any, []string, bool) {
	out := map[string]any{}
	sensitive := false
	for _, key := range slices.Sorted(maps.Keys(a.strs)) {
		if want := a.strs[key]; u.strs[key] != want {
			out[key] = nullable(want)
			sensitive = sensitive || key == "mobilePhone"
		}
	}
	if a.phones != nil && !slices.Equal(a.phones, orEmpty(u.BusinessPhones)) {
		out["businessPhones"] = a.phones
		sensitive = true
	}
	if len(a.org) > 0 {
		var cc, div string
		if o := u.EmployeeOrgData; o != nil {
			cc, div = deref(o.CostCenter), deref(o.Division)
		}
		wantCC, wantDiv := cc, div
		if v, ok := a.org[fieldCostCenter]; ok {
			wantCC = v
		}
		if v, ok := a.org[fieldDivision]; ok {
			wantDiv = v
		}
		if wantCC != cc || wantDiv != div {
			out["employeeOrgData"] = orgBody(wantCC, wantDiv)
		}
	}
	return out, slices.Sorted(maps.Keys(out)), sensitive
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// setManager writes the manager by $ref: manager_not_found when the id is
// not a user the tenant holds, "" when it was written or none is named.
func (p *provisioner) setManager(ctx context.Context, s session, id uuid.UUID, manager string) (string, error) {
	if strings.TrimSpace(manager) == "" {
		return "", nil
	}
	mgr, ok := objectID(manager)
	if !ok {
		return contracts.ReportedManagerNotFound, nil
	}
	err := p.client.put(ctx, s.cred, userPath(id, "/manager/$ref"), map[string]string{"@odata.id": graphAPIBase + userPath(mgr)})
	switch {
	case err == nil:
		return "", nil
	case isStatus(err, http.StatusNotFound):
		return contracts.ReportedManagerNotFound, nil
	}
	return "", err
}

// bringToSpec is the update path (Prepare's for a user it found, and
// Update's): the properties that differ and the manager, never the
// employeeId or the UPN. It answers the codes it reports
// (manager_not_found), the names it changed, and privilegedRefusal when the
// tenant refused a sensitive property.
func (p *provisioner) bringToSpec(ctx context.Context, s session, id uuid.UUID, u graphUser, spec contracts.ExternalSpec) ([]string, []string, error) {
	body, changed, sensitive := wantedAttributes(spec, usageLocation(spec, s.conn)).patch(u)
	if len(body) > 0 {
		err := p.client.patch(ctx, s.cred, userPath(id), body)
		switch {
		case err != nil && sensitive && isStatus(err, http.StatusForbidden):
			return nil, nil, privilegedRefusal()
		case err != nil:
			return nil, nil, err
		case sensitive:
			if err := p.sensitiveSucceeded(ctx, s); err != nil {
				return nil, nil, err
			}
		}
	}
	var reported []string
	code, err := p.setManager(ctx, s, id, spec.ManagerExternalID)
	if err != nil {
		return nil, nil, err
	}
	if code != "" {
		reported = append(reported, code)
	} else if spec.ManagerExternalID != "" {
		changed = append(changed, "manager")
	}
	return reported, changed, nil
}

// instant is day's civil date at hour:00 in the business zone (UTC without
// a business calendar): the hire day's 01:00, the end day's 23:00.
func (p *provisioner) instant(ctx context.Context, day time.Time, hour int) (time.Time, error) {
	zone := time.UTC
	if p.deps.Calendar != nil {
		z, err := p.deps.Calendar.Zone(ctx)
		if err != nil {
			return time.Time{}, fmt.Errorf("entra: read the business zone: %w", err)
		}
		zone = z
	}
	y, m, d := day.Date()
	return time.Date(y, m, d, hour, 0, 0, 0, zone), nil
}

// The codes a call reports beside the contract's (ExternalAccount.Reported).
const (
	// reportedLeaverStepFailed: a leaver's step after the disable failed; the
	// step's name follows the colon (revoke_sessions, leave_date,
	// strip_licences, remove_groups).
	reportedLeaverStepFailed = "leaver_step_failed"
	// reportedAdoptionUnconfirmed: the user is one the module found holding
	// the employee number, not one it created, and no holder of
	// entra:manage has confirmed its adoption; nothing is written to it
	// until one does (plan reading 8).
	reportedAdoptionUnconfirmed = "adoption_unconfirmed"
	// reportedPassForbidden: the tenant refused the pass's create alone (a
	// 403): the user is enabled all the same, and gets no pass. It is no
	// privileged refusal and does not lengthen the streak — the enable,
	// the sensitive action, was allowed.
	reportedPassForbidden = "pass_forbidden"
)

// trusted reports whether the module may write to row's user: one it
// created, or one it found whose adoption a holder of entra:manage
// confirmed (plan reading 8). A user Staff names that the module keeps no
// row for is not.
func trusted(row store.EntraAccount) bool {
	return row.CreatedByUs || row.AdoptionConfirmedAt != nil
}

// adoptionJudges is the code an unconfirmed adoption's answer judges.
func adoptionJudges(c string) bool { return c == reportedAdoptionUnconfirmed }

// unconfirmed refuses a write to u, an adopted user nobody has confirmed
// (plan reading 8), with contracts.ErrExternalUnconfirmed: nothing written
// to the tenant; the row keeps the state Graph answers and
// adoption_unconfirmed among its codes, for the Status card, its last error
// as it was; the write's event carries codes and
// refused:adoption_unconfirmed. A leaver's safety does not reach a user the
// module never owned: Deactivate and Delete are refused so too, and the
// caller, told so, keeps trying until the adoption is confirmed.
func (p *provisioner) unconfirmed(ctx context.Context, s session, actor contracts.Actor, action string, id uuid.UUID, u graphUser, row store.EntraAccount, codes ...string) error {
	state := stateDisabled
	if u.AccountEnabled {
		state = stateEnabled
	}
	if row.State == stateDeleted {
		state = stateDeleted
	}
	reported := mergeCodes(row.Reported, adoptionJudges, []string{reportedAdoptionUnconfirmed})
	now := p.client.now()
	err := db.WithTx(ctx, p.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		_, err := q.SetAccountState(ctx, store.SetAccountStateParams{
			State: state, Reported: orEmpty(reported), LastError: row.LastError, Now: now, ObjectID: id,
		})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			// No row: a user the module keeps none for, the event alone.
			return fmt.Errorf("entra: record the account: %w", err)
		}
		return recordEvent(ctx, q, now, actor, action, &id, u.UserPrincipalName, append(codes, "refused:"+reportedAdoptionUnconfirmed))
	})
	if err != nil {
		return err
	}
	return contracts.ErrExternalUnconfirmed
}

// codeSuffix is what may follow a code's colon (Staff's bound).
var codeSuffix = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

// licenceCode is licence_unavailable with the part number, when it has the
// form a code may carry.
func licenceCode(part string) string {
	if codeSuffix.MatchString(part) {
		return contracts.ReportedLicenceUnavailable + ":" + part
	}
	return contracts.ReportedLicenceUnavailable
}

// mergeCodes is old without the codes evaluated says this call judged, with
// fresh added: a call replaces what it knows about and keeps the rest — an
// Update leaves Activate's licence codes as they were.
func mergeCodes(old []string, evaluated func(string) bool, fresh []string) []string {
	out := []string{}
	for _, c := range old {
		if !evaluated(c) && !slices.Contains(out, c) {
			out = append(out, c)
		}
	}
	for _, c := range fresh {
		if c != "" && !slices.Contains(out, c) {
			out = append(out, c)
		}
	}
	return out
}

// photoJudges are the codes SetPhoto and ClearPhoto judge.
func photoJudges(c string) bool { return c == contracts.ReportedPhotoNotWritten }

// updateJudges are the codes Prepare's update path and Update judge.
func updateJudges(c string) bool { return c == contracts.ReportedManagerNotFound }

// licenceJudges are the codes a reconcile of the licences judges: Update's
// on an enabled user, besides updateJudges'.
func licenceJudges(c string) bool {
	return c == contracts.ReportedLicenceUnavailable || strings.HasPrefix(c, contracts.ReportedLicenceUnavailable+":")
}

// activateJudges are the codes Activate judges: the hire date, the
// licences, the pass, the role, and a leaver's step a rehire leaves behind.
func activateJudges(c string) bool {
	return c == contracts.ReportedHireDateNotWritten || c == contracts.ReportedPassNotAllowed || c == reportedPassForbidden ||
		c == contracts.ReportedPrivileged ||
		c == contracts.ReportedLicenceUnavailable || strings.HasPrefix(c, contracts.ReportedLicenceUnavailable+":") ||
		strings.HasPrefix(c, reportedLeaverStepFailed+":")
}
