package google

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/google/store"
)

// This file is the user as the lifecycle reads and writes it in the Admin
// SDK's Directory (Google Workspace design G17): the fields read, Staff's
// spec mapped to Google's attributes — only those Google has —, the
// difference an update writes, the manager, and the correlation on the
// organization-typed external id.

// The external id type the employee number is kept under, and the
// relation type of the manager.
const (
	externalIDOrganization = "organization"
	relationManager        = "manager"
)

// The Fields of a spec (Entra design D8's names) Google has an attribute for;
// every other field — employeeHireDate, employeeLeaveDateTime, employeeType,
// companyName, employeeOrgData's division, the postal address — is ignored
// and not reported: there is nothing an administrator can fix.
const (
	fieldJobTitle       = "jobTitle"
	fieldDepartment     = "department"
	fieldCostCenter     = "costCenter"
	fieldBusinessPhone  = "businessPhone"
	fieldMobilePhone    = "mobilePhone"
	fieldOfficeLocation = "officeLocation"
)

// The bounds of what reaches Google, in characters.
const (
	maxNamePart  = 60
	maxOrgValue  = 100
	maxPhone     = 64
	maxLocation  = 100
	maxFindPages = 5
)

// errNoSuchUser: the id is not a user the organisation holds. Staff reads
// Account before it decides, which answers nil and records user_gone.
var errNoSuchUser = errors.New("google: the organisation holds no such user")

// directoryUser is a user as the Directory answers it. The list-valued
// attributes are kept as Google's objects, so an update writes back what it
// does not own as it found it.
type directoryUser struct {
	ID               string `json:"id"`
	PrimaryEmail     string `json:"primaryEmail"`
	Suspended        bool   `json:"suspended"`
	IsAdmin          bool   `json:"isAdmin"`
	IsDelegatedAdmin bool   `json:"isDelegatedAdmin"`
	LastLoginTime    string `json:"lastLoginTime"`
	Name             struct {
		GivenName  string `json:"givenName"`
		FamilyName string `json:"familyName"`
	} `json:"name"`
	ExternalIDs   []map[string]any `json:"externalIds"`
	Organizations []map[string]any `json:"organizations"`
	Phones        []map[string]any `json:"phones"`
	Relations     []map[string]any `json:"relations"`
	Languages     []map[string]any `json:"languages"`
	Locations     []map[string]any `json:"locations"`
}

// privileged reports whether u holds an administrator role.
func (u directoryUser) privileged() bool { return u.IsAdmin || u.IsDelegatedAdmin }

// neverSignedIn reports whether u has never signed in: Google answers the
// epoch as its last sign-in.
func (u directoryUser) neverSignedIn() bool {
	return u.LastLoginTime == "" || strings.HasPrefix(u.LastLoginTime, "1970-01-01")
}

// holdsNumber reports whether u carries number as an organization-typed
// external id, ignoring case: the client-side filter over what
// query=externalId: answers (U11), which may match another type or a part.
func (u directoryUser) holdsNumber(number string) bool {
	return slices.ContainsFunc(u.ExternalIDs, func(e map[string]any) bool {
		return str(e["type"]) == externalIDOrganization && strings.EqualFold(strings.TrimSpace(str(e["value"])), number)
	})
}

// str is v as a string, "" for anything else.
func str(v any) string {
	s, _ := v.(string)
	return s
}

// userURL is a user's URL in the Directory, by its id, with rest after it.
func userURL(id string, rest ...string) string {
	return directoryBase + "/users/" + url.PathEscape(id) + strings.Join(rest, "")
}

// getUser reads the user with id; errNoSuchUser (wrapped) on a 404.
func (p *provisioner) getUser(ctx context.Context, s session, id string) (directoryUser, error) {
	if !validUserID(id) {
		return directoryUser{}, errNoSuchUser
	}
	var u directoryUser
	err := p.keys.c.get(ctx, s.users, userURL(id, "?projection=basic"), &u)
	if errors.Is(err, errNotFound) {
		return directoryUser{}, fmt.Errorf("%w: %w", errNoSuchUser, err)
	}
	return u, err
}

// bareNumber is an employee number the search takes unquoted.
var bareNumber = regexp.MustCompile(`^[A-Za-z0-9-]+$`)

// numberQuery is the Directory's search for number among the external ids:
// bare when it is letters, digits and hyphens, as Staff's numbers are, else
// quoted.
func numberQuery(number string) string {
	if bareNumber.MatchString(number) {
		return "externalId:" + number
	}
	return "externalId:'" + strings.ReplaceAll(strings.ReplaceAll(number, `\`, `\\`), "'", `\'`) + "'"
}

// findByNumber lists the users holding number as their organization
// external id: users.list(customer=my_customer, query=externalId:<number>),
// at most maxFindPages pages, each answer filtered client-side (U11).
func (p *provisioner) findByNumber(ctx context.Context, s session, number string) ([]directoryUser, error) {
	var out []directoryUser
	token := ""
	for range maxFindPages {
		v := url.Values{"customer": {"my_customer"}, "query": {numberQuery(number)}, "maxResults": {"100"}, "projection": {"basic"}}
		if token != "" {
			v.Set("pageToken", token)
		}
		var page struct {
			Users         []directoryUser `json:"users"`
			NextPageToken string          `json:"nextPageToken"`
		}
		if err := p.keys.c.get(ctx, s.users, directoryBase+"/users?"+v.Encode(), &page); err != nil {
			return nil, err
		}
		for _, u := range page.Users {
			if u.holdsNumber(number) {
				out = append(out, u)
			}
		}
		if token = page.NextPageToken; token == "" {
			break
		}
	}
	return out, nil
}

// ensureRow is the account's row, made from u when the module has none yet
// — a user Staff linked before this row existed — as one the module found,
// not created, under number.
func (p *provisioner) ensureRow(ctx context.Context, s session, u directoryUser, number string) (store.GoogleAccount, error) {
	row, err := s.q.GetAccount(ctx, u.ID)
	if err == nil {
		return row, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return store.GoogleAccount{}, fmt.Errorf("google: read the account: %w", err)
	}
	state := stateEnabled
	if u.Suspended {
		state = stateDisabled
	}
	if number == "" {
		// A call without a spec: nothing to key a row by, so none is made.
		return store.GoogleAccount{UserID: u.ID, PrimaryEmail: u.PrimaryEmail, State: state, Reported: []string{}}, nil
	}
	row, err = s.q.UpsertAccount(ctx, store.UpsertAccountParams{
		UserID: u.ID, EmployeeNumber: number, PrimaryEmail: u.PrimaryEmail, State: state,
		CreatedByUs: false, Reported: []string{}, Now: p.now(),
	})
	if err != nil {
		return store.GoogleAccount{}, fmt.Errorf("google: record the account: %w", err)
	}
	return row, nil
}

// profile is what a spec asks of a user's attributes: the names, and each
// list-valued attribute the spec has a say in, by Google's name.
type profile struct {
	given, family string
	// fields are the spec's fields Google has, "" to clear one; a field the
	// installation turned off is absent, and left as it is.
	fields   map[string]string
	language string // Google's code, "" for none named
}

// wantedProfile maps spec to Google's attributes, each value cut to what
// Google keeps.
func wantedProfile(spec contracts.ExternalSpec) profile {
	pr := profile{fields: map[string]string{}}
	pr.given = cut(strings.TrimSpace(spec.GivenName), maxNamePart)
	pr.family = cut(strings.TrimSpace(spec.FamilyName), maxNamePart)
	switch {
	case pr.given == "" && pr.family == "":
		pr.given, pr.family = cut(strings.TrimSpace(spec.DisplayName), maxNamePart), "-"
	case pr.family == "":
		pr.family = pr.given
	case pr.given == "":
		pr.given = pr.family
	}
	if pr.given == "" {
		pr.given = cut(strings.TrimSpace(spec.EmployeeNumber), maxNamePart)
	}
	for key, v := range spec.Fields {
		v = strings.TrimSpace(v)
		switch key {
		case fieldJobTitle, fieldDepartment, fieldCostCenter:
			pr.fields[key] = cut(v, maxOrgValue)
		case fieldBusinessPhone, fieldMobilePhone:
			pr.fields[key] = cut(v, maxPhone)
		case fieldOfficeLocation:
			pr.fields[key] = cut(v, maxLocation)
		}
	}
	switch strings.ToLower(strings.TrimSpace(spec.PreferredLanguage)) {
	case "nb", "no", "nn", "nb-no":
		pr.language = "no"
	case "en", "en-gb":
		pr.language = "en-GB"
	}
	return pr
}

// cut is s at most n characters long.
func cut(s string, n int) string {
	if n <= 0 || utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// organizations is u's organisations with the primary one carrying the
// profile's title, department and cost centre: the fields the profile
// names written ("" removed), the rest of every entry as Google had it.
func (pr profile) organizations(current []map[string]any) []map[string]any {
	keys := map[string]string{fieldJobTitle: "title", fieldDepartment: "department", fieldCostCenter: "costCenter"}
	named := false
	for f := range keys {
		if _, ok := pr.fields[f]; ok {
			named = true
		}
	}
	out := cloneEntries(current)
	if !named {
		return out
	}
	i := slices.IndexFunc(out, func(e map[string]any) bool { return e["primary"] == true })
	if i < 0 && len(out) > 0 {
		i = 0
	}
	if i < 0 {
		if !slices.ContainsFunc(slices.Collect(maps.Keys(keys)), func(f string) bool { return pr.fields[f] != "" }) {
			return out
		}
		out = append(out, map[string]any{"primary": true, "type": "work"})
		i = len(out) - 1
	}
	for f, attr := range keys {
		v, ok := pr.fields[f]
		switch {
		case !ok:
		case v == "":
			delete(out[i], attr)
		default:
			out[i][attr] = v
		}
	}
	return out
}

// typed is current with the entries of type replaced by one carrying value
// under attr (none when value is ""), when the profile names field; current
// as it is otherwise.
func (pr profile) typed(current []map[string]any, field, typ, attr string, extra map[string]any) []map[string]any {
	v, ok := pr.fields[field]
	out := cloneEntries(current)
	if !ok {
		return out
	}
	out = slices.DeleteFunc(out, func(e map[string]any) bool { return str(e["type"]) == typ })
	if v != "" {
		e := map[string]any{"type": typ, attr: v}
		maps.Copy(e, extra)
		out = append(out, e)
	}
	return out
}

// cloneEntries is a copy of entries one level deep.
func cloneEntries(entries []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		out = append(out, maps.Clone(e))
	}
	return out
}

// managerRelations is current with the manager relation naming email.
func managerRelations(current []map[string]any, email string) []map[string]any {
	out := slices.DeleteFunc(cloneEntries(current), func(e map[string]any) bool { return str(e["type"]) == relationManager })
	return append(out, map[string]any{"type": relationManager, "value": email})
}

// patch is what an update writes to u: each attribute whose value differs,
// by Google's name, and the names changed in order. manager is the
// manager's primary email, "" to leave the relation as it is.
func (pr profile) patch(u directoryUser, manager string) (map[string]any, []string) {
	out := map[string]any{}
	if u.Name.GivenName != pr.given || u.Name.FamilyName != pr.family {
		out["name"] = map[string]any{"givenName": pr.given, "familyName": pr.family}
	}
	lists := map[string][2][]map[string]any{
		"organizations": {u.Organizations, pr.organizations(u.Organizations)},
		"phones": {u.Phones, pr.typed(pr.typed(u.Phones, fieldBusinessPhone, "work", "value", nil),
			fieldMobilePhone, "mobile", "value", nil)},
		"locations": {u.Locations, pr.typed(u.Locations, fieldOfficeLocation, "desk", "area", nil)},
	}
	if manager != "" {
		lists["relations"] = [2][]map[string]any{u.Relations, managerRelations(u.Relations, manager)}
	}
	if pr.language != "" {
		lists["languages"] = [2][]map[string]any{u.Languages, {{"languageCode": pr.language, "preference": "preferred"}}}
	}
	for name, l := range lists {
		if !sameEntries(l[0], l[1]) {
			out[name] = l[1]
		}
	}
	return out, slices.Sorted(maps.Keys(out))
}

// sameEntries reports whether a and b hold the same entries in the same
// order, compared as JSON values.
func sameEntries(a, b []map[string]any) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	var va, vb any
	_ = json.Unmarshal(ja, &va)
	_ = json.Unmarshal(jb, &vb)
	return reflect.DeepEqual(va, vb)
}

// createBody is users.insert's body for the profile: the attributes it
// names, beside what the create sets itself.
func (pr profile) createBody(body map[string]any, manager string) {
	body["name"] = map[string]any{"givenName": pr.given, "familyName": pr.family}
	if orgs := pr.organizations(nil); len(orgs) > 0 {
		body["organizations"] = orgs
	}
	if phones := pr.typed(pr.typed(nil, fieldBusinessPhone, "work", "value", nil), fieldMobilePhone, "mobile", "value", nil); len(phones) > 0 {
		body["phones"] = phones
	}
	if locs := pr.typed(nil, fieldOfficeLocation, "desk", "area", nil); len(locs) > 0 {
		body["locations"] = locs
	}
	if manager != "" {
		body["relations"] = managerRelations(nil, manager)
	}
	if pr.language != "" {
		body["languages"] = []map[string]any{{"languageCode": pr.language, "preference": "preferred"}}
	}
}

// managerEmail is the primary email of the manager the spec names by their
// Google id: the module's own row first, else the Directory.
// manager_not_found when the organisation holds no such user; "" and no
// code when none is named.
func (p *provisioner) managerEmail(ctx context.Context, s session, managerID string) (string, string, error) {
	managerID = strings.TrimSpace(managerID)
	if managerID == "" {
		return "", "", nil
	}
	if !validUserID(managerID) {
		return "", contracts.ReportedManagerNotFound, nil
	}
	if row, err := s.q.GetAccount(ctx, managerID); err == nil && row.State != stateDeleted && row.PrimaryEmail != "" {
		return row.PrimaryEmail, "", nil
	} else if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", "", fmt.Errorf("google: read the manager's account: %w", err)
	}
	m, err := p.getUser(ctx, s, managerID)
	switch {
	case errors.Is(err, errNoSuchUser):
		return "", contracts.ReportedManagerNotFound, nil
	case err != nil:
		return "", "", err
	}
	return m.PrimaryEmail, "", nil
}

// bringToSpec is the update path (Prepare's for a user it found, and
// Update's): the attributes that differ and the manager, never the external
// id or the primary email. It answers the codes it reports
// (manager_not_found) and the names it changed; a 403 is the user an
// administrator's (privilegedRefusal).
func (p *provisioner) bringToSpec(ctx context.Context, s session, u directoryUser, spec contracts.ExternalSpec) ([]string, []string, error) {
	manager, code, err := p.managerEmail(ctx, s, spec.ManagerExternalID)
	if err != nil {
		return nil, nil, err
	}
	var reported []string
	if code != "" {
		reported = append(reported, code)
	}
	body, changed := wantedProfile(spec).patch(u, manager)
	if len(body) == 0 {
		return reported, nil, nil
	}
	err = p.keys.c.send(ctx, s.users, http.MethodPatch, userURL(u.ID), body, nil)
	if isStatus(err, http.StatusForbidden) {
		return nil, nil, privilegedRefusal()
	}
	if err != nil {
		return nil, nil, err
	}
	return reported, changed, nil
}

// isStatus reports whether err is Google's refusal with status.
func isStatus(err error, status int) bool {
	var g *googleError
	return errors.As(err, &g) && g.Status == status
}

// isReason reports whether err is Google's refusal with reason.
func isReason(err error, reason string) bool {
	var g *googleError
	return errors.As(err, &g) && g.Reason == reason
}

// isRefusal reports whether err is Google's 4xx refusal.
func isRefusal(err error) bool {
	var g *googleError
	return errors.As(err, &g) && g.Status >= 400 && g.Status < 500
}
