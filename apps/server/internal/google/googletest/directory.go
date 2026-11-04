package googletest

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

// This file is the fake's Directory users and groups, its Groups Settings
// and its Enterprise License Manager (delivery B): what the provisioner
// calls, each behind the scope Google asks of it.
//
// The user search answers as loosely as Google may (U11): externalId:<n>
// matches any external id of any type whose value starts with n, ignoring
// case, so the module's own filter to an organization id equal to the
// number is what decides. With SearchLag it lags a create, as Google's
// index does: a user users.insert made is not found by the search until
// CatchUp, while users.get answers it at once by its id or its address.

// User is a user in the fake's Directory. The list-valued attributes are
// Google's objects, as a test sets them and as the module wrote them.
type User struct {
	ID           string
	PrimaryEmail string
	Aliases      []string
	// NonEditableAliases are the same names on an alias domain, which
	// Google answers apart from the aliases.
	NonEditableAliases []string
	// OtherEmails are emails[] entries beyond the primary email and the
	// aliases — an address an administrator typed for the person, such as
	// a home address elsewhere ({"address": …, "type": "home"}).
	OtherEmails               []map[string]any
	GivenName, FamilyName     string
	Suspended                 bool
	IsAdmin, IsDelegatedAdmin bool
	// LastLoginTime is when the user last signed in, RFC 3339; the epoch
	// (Google's answer for never) when empty.
	LastLoginTime             string
	OrgUnitPath               string
	Password                  string
	ChangePasswordAtNextLogin bool
	ExternalIDs               []map[string]any
	Organizations             []map[string]any
	Phones                    []map[string]any
	Relations                 []map[string]any
	Languages                 []map[string]any
	Locations                 []map[string]any
	// Photo is the photo written, decoded from Google's web-safe base64,
	// and its mime type and size as given.
	Photo         []byte
	PhotoMimeType string
	PhotoWidth    int
	SignedOut     int
}

// Group is a group in the fake's Directory, with its members by user id
// (their role) and its Groups Settings.
type Group struct {
	ID, Email, Name, Description string
	Members                      map[string]string
	Settings                     map[string]any
}

// directory is the fake's users, groups and licences, under the fake's
// lock.
type directory struct {
	users  map[string]*User
	groups map[string]*Group
	nextID int
	// assignments are the licences held, by productId/skuId, each a set of
	// users' primary emails (lower-case).
	assignments map[string]map[string]bool
	// seats are the licences left, by productId/skuId; a licence without
	// an entry has seats enough.
	seats map[string]int
	// products are the product ids listForProduct knows; another answers
	// 400, as Google does for a product id it does not know.
	products []string
	// skuNames are the SKUs' names listForProduct answers.
	skuNames map[string]string
	// RefuseSignOut makes users.signOut answer 403 (U19).
	RefuseSignOut bool
	// RefusePhoto makes users.photos.update answer 400 (U13).
	RefusePhoto bool
	// RefusePassword makes a write carrying a password answer 400, as an
	// organisation's password policy may.
	RefusePassword bool
	// SearchLag makes the users users.insert makes unfound by users.list
	// until CatchUp (lagging holds their ids).
	SearchLag bool
	lagging   map[string]bool
	// userLists counts the users.list calls answered.
	userLists int
}

// CatchUp makes the search find every user it lagged.
func (g *FakeGoogle) CatchUp() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.lagging = nil
}

// AddUser puts u in the Directory, its id made when empty, and answers it.
func (g *FakeGoogle) AddUser(u User) User {
	g.mu.Lock()
	defer g.mu.Unlock()
	return *g.addUser(u)
}

func (g *FakeGoogle) addUser(u User) *User {
	if g.users == nil {
		g.users = map[string]*User{}
	}
	if u.ID == "" {
		g.nextID++
		u.ID = fmt.Sprintf("1%020d", g.nextID)
	}
	u.PrimaryEmail = strings.ToLower(u.PrimaryEmail)
	if u.OrgUnitPath == "" {
		u.OrgUnitPath = "/"
	}
	c := u
	g.users[u.ID] = &c
	return &c
}

// User is the user with id, false when the Directory holds none.
func (g *FakeGoogle) User(id string) (User, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	u, ok := g.users[id]
	if !ok {
		return User{}, false
	}
	return *u, true
}

// UserByEmail is the user whose primary email is email.
func (g *FakeGoogle) UserByEmail(email string) (User, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, u := range g.users {
		if strings.EqualFold(u.PrimaryEmail, email) {
			return *u, true
		}
	}
	return User{}, false
}

// SetUser changes the user with id through fn.
func (g *FakeGoogle) SetUser(id string, fn func(u *User)) {
	g.Configure(func(g *FakeGoogle) {
		if u, ok := g.users[id]; ok {
			fn(u)
		}
	})
}

// DeleteUser removes the user with id, as an administrator would.
func (g *FakeGoogle) DeleteUser(id string) {
	g.Configure(func(g *FakeGoogle) { delete(g.users, id) })
}

// AddGroup puts gr in the Directory, its id made when empty, and answers it.
func (g *FakeGoogle) AddGroup(gr Group) Group {
	g.mu.Lock()
	defer g.mu.Unlock()
	return *g.addGroup(gr)
}

func (g *FakeGoogle) addGroup(gr Group) *Group {
	if g.groups == nil {
		g.groups = map[string]*Group{}
	}
	if gr.ID == "" {
		g.nextID++
		gr.ID = fmt.Sprintf("0%014x", g.nextID)
	}
	gr.Email = strings.ToLower(gr.Email)
	if gr.Members == nil {
		gr.Members = map[string]string{}
	}
	if gr.Settings == nil {
		gr.Settings = map[string]any{}
	}
	c := gr
	g.groups[gr.ID] = &c
	return &c
}

// Group is the group with id, false when the Directory holds none.
func (g *FakeGoogle) Group(id string) (Group, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	gr, ok := g.groups[id]
	if !ok {
		return Group{}, false
	}
	c := *gr
	c.Members = maps.Clone(gr.Members)
	c.Settings = maps.Clone(gr.Settings)
	return c, true
}

// GroupByEmail is the group whose address is email.
func (g *FakeGoogle) GroupByEmail(email string) (Group, bool) {
	g.mu.Lock()
	var id string
	for _, gr := range g.groups {
		if strings.EqualFold(gr.Email, email) {
			id = gr.ID
		}
	}
	g.mu.Unlock()
	if id == "" {
		return Group{}, false
	}
	return g.Group(id)
}

// Products sets the product ids listForProduct knows, and the SKUs' names.
func (g *FakeGoogle) Products(products []string, skuNames map[string]string) {
	g.Configure(func(g *FakeGoogle) { g.products, g.skuNames = products, skuNames })
}

// Seats sets how many of licence (productId/skuId) are left to assign.
func (g *FakeGoogle) Seats(licence string, n int) {
	g.Configure(func(g *FakeGoogle) {
		if g.seats == nil {
			g.seats = map[string]int{}
		}
		g.seats[licence] = n
	})
}

// Assign gives the user email licence, as an administrator would.
func (g *FakeGoogle) Assign(licence, email string) {
	g.Configure(func(g *FakeGoogle) { g.assign(licence, email) })
}

func (g *FakeGoogle) assign(licence, email string) {
	if g.assignments == nil {
		g.assignments = map[string]map[string]bool{}
	}
	if g.assignments[licence] == nil {
		g.assignments[licence] = map[string]bool{}
	}
	g.assignments[licence][strings.ToLower(email)] = true
}

// Holds reports whether the user email holds licence.
func (g *FakeGoogle) Holds(licence, email string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.assignments[licence][strings.ToLower(email)]
}

// directoryRoute is the scope and the handler of a Directory, Groups
// Settings or Licensing call; a nil handler for a path the fake does not
// know.
func (g *FakeGoogle) directoryRoute(req *http.Request, body []byte) (string, func() answer) {
	path, method := req.URL.Path, req.Method
	switch req.URL.Host {
	case APIsHost:
		if rest, ok := strings.CutPrefix(path, "/groups/v1/groups/"); ok && method == http.MethodPatch {
			return ScopeGroupsSettings, func() answer { return g.groupSettings(rest, body) }
		}
		return "", nil
	case LicensingHost:
		return g.licensingRoute(req, body)
	}
	rest, ok := strings.CutPrefix(path, "/admin/directory/v1/")
	if !ok {
		return "", nil
	}
	parts := strings.Split(rest, "/")
	switch {
	case parts[0] == "users" && len(parts) == 1 && method == http.MethodGet:
		return ScopeUsers, func() answer { return g.listUsers(req.URL.Query()) }
	case parts[0] == "users" && len(parts) == 1 && method == http.MethodPost:
		return ScopeUsers, func() answer { return g.insertUser(body) }
	case parts[0] == "users" && len(parts) == 2:
		id := parts[1]
		switch method {
		case http.MethodGet:
			return ScopeUsers, func() answer { return g.getUser(id) }
		case http.MethodPatch:
			return ScopeUsers, func() answer { return g.patchUser(id, body) }
		case http.MethodDelete:
			return ScopeUsers, func() answer { return g.deleteUser(id) }
		}
	case parts[0] == "users" && len(parts) == 3 && parts[2] == "signOut" && method == http.MethodPost:
		return ScopeUsers, func() answer { return g.signOut(parts[1]) }
	case parts[0] == "users" && len(parts) == 4 && parts[2] == "photos" && parts[3] == "thumbnail":
		switch method {
		case http.MethodPut:
			return ScopeUsers, func() answer { return g.setPhoto(parts[1], body) }
		case http.MethodDelete:
			return ScopeUsers, func() answer { return g.clearPhoto(parts[1]) }
		}
	case parts[0] == "groups" && len(parts) == 1 && method == http.MethodGet:
		return ScopeGroups, func() answer { return g.listGroups(req.URL.Query()) }
	case parts[0] == "groups" && len(parts) == 1 && method == http.MethodPost:
		return ScopeGroups, func() answer { return g.insertGroup(body) }
	case parts[0] == "groups" && len(parts) == 2:
		key, _ := url.PathUnescape(parts[1])
		switch method {
		case http.MethodGet:
			return ScopeGroups, func() answer { return g.getGroup(key) }
		case http.MethodPatch:
			return ScopeGroups, func() answer { return g.patchGroup(key, body) }
		case http.MethodDelete:
			return ScopeGroups, func() answer { return g.deleteGroup(key) }
		}
	case parts[0] == "groups" && len(parts) == 3 && parts[2] == "members":
		switch method {
		case http.MethodGet:
			return ScopeGroups, func() answer { return g.listMembers(parts[1], req.URL.Query()) }
		case http.MethodPost:
			return ScopeGroups, func() answer { return g.insertMember(parts[1], body) }
		}
	case parts[0] == "groups" && len(parts) == 4 && parts[2] == "members":
		switch method {
		case http.MethodPatch:
			return ScopeGroups, func() answer { return g.patchMember(parts[1], parts[3], body) }
		case http.MethodDelete:
			return ScopeGroups, func() answer { return g.deleteMember(parts[1], parts[3]) }
		}
	}
	return "", nil
}

func (g *FakeGoogle) notFound() answer {
	return answer{status: http.StatusNotFound, body: g.apiError(http.StatusNotFound, "notFound")}
}

func (g *FakeGoogle) refuse(status int, reason string) answer {
	return answer{status: status, body: g.apiError(status, reason)}
}

// userBody is u as the Directory answers it.
func userBody(u *User) map[string]any {
	last := u.LastLoginTime
	if last == "" {
		last = "1970-01-01T00:00:00.000Z"
	}
	out := map[string]any{
		"kind": "admin#directory#user", "id": u.ID, "primaryEmail": u.PrimaryEmail,
		"name":      map[string]any{"givenName": u.GivenName, "familyName": u.FamilyName, "fullName": u.GivenName + " " + u.FamilyName},
		"suspended": u.Suspended, "isAdmin": u.IsAdmin, "isDelegatedAdmin": u.IsDelegatedAdmin,
		"lastLoginTime": last, "orgUnitPath": u.OrgUnitPath, "changePasswordAtNextLogin": u.ChangePasswordAtNextLogin,
	}
	for key, v := range map[string][]map[string]any{
		"externalIds": u.ExternalIDs, "organizations": u.Organizations, "phones": u.Phones,
		"relations": u.Relations, "languages": u.Languages, "locations": u.Locations,
	} {
		if len(v) > 0 {
			out[key] = v
		}
	}
	if len(u.Aliases) > 0 {
		out["aliases"] = u.Aliases
	}
	if len(u.NonEditableAliases) > 0 {
		out["nonEditableAliases"] = u.NonEditableAliases
	}
	emails := []map[string]any{{"address": u.PrimaryEmail, "primary": true}}
	for _, a := range u.Aliases {
		emails = append(emails, map[string]any{"address": a})
	}
	out["emails"] = append(emails, u.OtherEmails...)
	return out
}

// fullName is u's full name as Google answers it.
func (u *User) fullName() string { return strings.TrimSpace(u.GivenName + " " + u.FamilyName) }

// emailTaken reports whether email is a user's primary email or alias, or a
// group's address.
func (g *FakeGoogle) emailTaken(email string) bool {
	for _, u := range g.users {
		if strings.EqualFold(u.PrimaryEmail, email) || slices.ContainsFunc(u.Aliases, func(a string) bool { return strings.EqualFold(a, email) }) {
			return true
		}
	}
	for _, gr := range g.groups {
		if strings.EqualFold(gr.Email, email) {
			return true
		}
	}
	return false
}

// listUsers is users.list for customer=my_customer, paged by maxResults (at
// most 500, 100 when absent) and pageToken, ordered by id, with one query
// or none:
//
//   - externalId:<n> (bare or quoted), matched loosely (U11);
//   - name:'<s>*', the full name starting with s, ignoring case;
//   - email:'<s>*', the primary email or an alias starting with s, ignoring
//     case;
//
// and any other query refused 400 invalidQuery, as an OR between two
// clauses would be here.
func (g *FakeGoogle) listUsers(q url.Values) answer {
	if q.Get("customer") != "my_customer" {
		return g.refuse(http.StatusBadRequest, "badRequest")
	}
	match, ok := userQuery(q.Get("query"))
	if !ok {
		return g.refuse(http.StatusBadRequest, "invalidQuery")
	}
	size := 100
	if v := q.Get("maxResults"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 500 {
			return g.refuse(http.StatusBadRequest, "invalid")
		}
		size = n
	}
	offset := 0
	if v := q.Get("pageToken"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return g.refuse(http.StatusBadRequest, "invalid")
		}
		offset = n
	}
	ids := slices.Sorted(maps.Keys(g.users))
	var found []*User
	for _, id := range ids {
		u := g.users[id]
		if !g.lagging[id] && match(u) {
			found = append(found, u)
		}
	}
	g.userLists++
	body := map[string]any{"kind": "admin#directory#users"}
	var out []map[string]any
	for i := offset; i < len(found) && i < offset+size; i++ {
		out = append(out, userBody(found[i]))
	}
	if len(out) > 0 {
		body["users"] = out
	}
	if offset+size < len(found) {
		body["nextPageToken"] = strconv.Itoa(offset + size)
	}
	return answer{status: http.StatusOK, body: body}
}

// userQuery is users.list's query as the fake reads it: the users it
// matches; false for a query the fake refuses.
func userQuery(query string) (func(u *User) bool, bool) {
	prefix := func(field string) (string, bool) {
		rest, ok := strings.CutPrefix(query, field+":'")
		if !ok {
			return "", false
		}
		s, ok := strings.CutSuffix(rest, "*'")
		if !ok || s == "" || strings.ContainsAny(s, `'"*\`) {
			return "", false
		}
		return strings.ToLower(s), true
	}
	if query == "" {
		return func(*User) bool { return true }, true
	}
	if number, ok := strings.CutPrefix(query, "externalId:"); ok {
		number = strings.ToLower(strings.Trim(number, "'"))
		return func(u *User) bool {
			return slices.ContainsFunc(u.ExternalIDs, func(e map[string]any) bool {
				v, _ := e["value"].(string)
				return strings.HasPrefix(strings.ToLower(v), number)
			})
		}, true
	}
	if s, ok := prefix("name"); ok {
		return func(u *User) bool { return strings.HasPrefix(strings.ToLower(u.fullName()), s) }, true
	}
	if s, ok := prefix("email"); ok {
		return func(u *User) bool {
			return strings.HasPrefix(u.PrimaryEmail, s) ||
				slices.ContainsFunc(u.Aliases, func(a string) bool { return strings.HasPrefix(strings.ToLower(a), s) })
		}, true
	}
	return nil, false
}

// UserLists is how many users.list calls the fake answered.
func (g *FakeGoogle) UserLists() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.userLists
}

// attributes copies the list-valued attributes and the flags body names
// onto u.
func applyUser(u *User, in map[string]any) {
	list := func(key string) ([]map[string]any, bool) {
		raw, ok := in[key]
		if !ok {
			return nil, false
		}
		b, _ := json.Marshal(raw)
		var out []map[string]any
		_ = json.Unmarshal(b, &out)
		return out, true
	}
	if v, ok := list("externalIds"); ok {
		u.ExternalIDs = v
	}
	if v, ok := list("organizations"); ok {
		u.Organizations = v
	}
	if v, ok := list("phones"); ok {
		u.Phones = v
	}
	if v, ok := list("relations"); ok {
		u.Relations = v
	}
	if v, ok := list("languages"); ok {
		u.Languages = v
	}
	if v, ok := list("locations"); ok {
		u.Locations = v
	}
	if n, ok := in["name"].(map[string]any); ok {
		if v, ok := n["givenName"].(string); ok {
			u.GivenName = v
		}
		if v, ok := n["familyName"].(string); ok {
			u.FamilyName = v
		}
	}
	if v, ok := in["suspended"].(bool); ok {
		u.Suspended = v
	}
	if v, ok := in["password"].(string); ok {
		u.Password = v
	}
	if v, ok := in["changePasswordAtNextLogin"].(bool); ok {
		u.ChangePasswordAtNextLogin = v
	}
	if v, ok := in["orgUnitPath"].(string); ok {
		u.OrgUnitPath = v
	}
}

func (g *FakeGoogle) insertUser(body []byte) answer {
	var in map[string]any
	if json.Unmarshal(body, &in) != nil {
		return g.refuse(http.StatusBadRequest, "invalid")
	}
	email, _ := in["primaryEmail"].(string)
	pw, _ := in["password"].(string)
	n, _ := in["name"].(map[string]any)
	if email == "" || pw == "" || n == nil || n["givenName"] == "" || n["familyName"] == "" {
		return g.refuse(http.StatusBadRequest, "required")
	}
	if g.emailTaken(email) {
		return g.refuse(http.StatusConflict, "duplicate")
	}
	u := g.addUser(User{PrimaryEmail: email})
	applyUser(u, in)
	if g.SearchLag {
		if g.lagging == nil {
			g.lagging = map[string]bool{}
		}
		g.lagging[u.ID] = true
	}
	return answer{status: http.StatusOK, body: userBody(u)}
}

// getUser is users.get: the userKey an id, a primary email or an alias.
func (g *FakeGoogle) getUser(key string) answer {
	u, ok := g.users[key]
	if !ok {
		for _, one := range g.users {
			if strings.EqualFold(one.PrimaryEmail, key) || slices.ContainsFunc(one.Aliases, func(a string) bool { return strings.EqualFold(a, key) }) {
				u, ok = one, true
				break
			}
		}
	}
	if !ok {
		return g.notFound()
	}
	return answer{status: http.StatusOK, body: userBody(u)}
}

func (g *FakeGoogle) patchUser(id string, body []byte) answer {
	u, ok := g.users[id]
	if !ok {
		return g.notFound()
	}
	var in map[string]any
	if json.Unmarshal(body, &in) != nil {
		return g.refuse(http.StatusBadRequest, "invalid")
	}
	if _, pw := in["password"]; pw && g.RefusePassword {
		return g.refuse(http.StatusBadRequest, "invalidPassword")
	}
	if _, email := in["primaryEmail"]; email {
		return g.refuse(http.StatusBadRequest, "renameRefusedByTheFake")
	}
	applyUser(u, in)
	return answer{status: http.StatusOK, body: userBody(u)}
}

func (g *FakeGoogle) deleteUser(id string) answer {
	if _, ok := g.users[id]; !ok {
		return g.notFound()
	}
	delete(g.users, id)
	return answer{status: http.StatusNoContent}
}

func (g *FakeGoogle) signOut(id string) answer {
	u, ok := g.users[id]
	if !ok {
		return g.notFound()
	}
	if g.RefuseSignOut {
		return g.refuse(http.StatusForbidden, "forbidden")
	}
	u.SignedOut++
	return answer{status: http.StatusNoContent}
}

func (g *FakeGoogle) setPhoto(id string, body []byte) answer {
	u, ok := g.users[id]
	if !ok {
		return g.notFound()
	}
	if g.RefusePhoto {
		return g.refuse(http.StatusBadRequest, "invalid")
	}
	var in struct {
		PhotoData string `json:"photoData"`
		MimeType  string `json:"mimeType"`
		Width     int    `json:"width"`
	}
	if json.Unmarshal(body, &in) != nil {
		return g.refuse(http.StatusBadRequest, "invalid")
	}
	std := strings.NewReplacer("-", "+", "_", "/", "*", "=").Replace(in.PhotoData)
	data, err := base64.StdEncoding.DecodeString(std)
	if err != nil {
		return g.refuse(http.StatusBadRequest, "invalid")
	}
	u.Photo, u.PhotoMimeType, u.PhotoWidth = data, in.MimeType, in.Width
	return answer{status: http.StatusOK, body: map[string]any{"kind": "admin#directory#user#photo", "id": u.ID}}
}

func (g *FakeGoogle) clearPhoto(id string) answer {
	u, ok := g.users[id]
	if !ok || u.Photo == nil {
		return g.notFound()
	}
	u.Photo, u.PhotoMimeType, u.PhotoWidth = nil, "", 0
	return answer{status: http.StatusNoContent}
}

// group is the group whose id or address is key.
func (g *FakeGoogle) group(key string) *Group {
	if gr, ok := g.groups[key]; ok {
		return gr
	}
	for _, gr := range g.groups {
		if strings.EqualFold(gr.Email, key) {
			return gr
		}
	}
	return nil
}

func groupBody(gr *Group) map[string]any {
	return map[string]any{
		"kind": "admin#directory#group", "id": gr.ID, "email": gr.Email, "name": gr.Name,
		"description": gr.Description, "directMembersCount": strconv.Itoa(len(gr.Members)),
	}
}

// listGroups is groups.list(userKey): the groups the user is a member of.
func (g *FakeGoogle) listGroups(q url.Values) answer {
	user := q.Get("userKey")
	if user == "" {
		return g.refuse(http.StatusBadRequest, "badRequest")
	}
	var out []map[string]any
	for _, id := range slices.Sorted(maps.Keys(g.groups)) {
		if _, ok := g.groups[id].Members[user]; ok {
			out = append(out, groupBody(g.groups[id]))
		}
	}
	body := map[string]any{"kind": "admin#directory#groups"}
	if len(out) > 0 {
		body["groups"] = out
	}
	return answer{status: http.StatusOK, body: body}
}

func (g *FakeGoogle) insertGroup(body []byte) answer {
	var in struct {
		Email       string `json:"email"`
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if json.Unmarshal(body, &in) != nil || in.Email == "" {
		return g.refuse(http.StatusBadRequest, "required")
	}
	if g.emailTaken(in.Email) {
		return g.refuse(http.StatusConflict, "duplicate")
	}
	gr := g.addGroup(Group{Email: in.Email, Name: in.Name, Description: in.Description})
	return answer{status: http.StatusOK, body: groupBody(gr)}
}

func (g *FakeGoogle) getGroup(key string) answer {
	gr := g.group(key)
	if gr == nil {
		return g.notFound()
	}
	return answer{status: http.StatusOK, body: groupBody(gr)}
}

func (g *FakeGoogle) patchGroup(key string, body []byte) answer {
	gr := g.group(key)
	if gr == nil {
		return g.notFound()
	}
	var in map[string]any
	if json.Unmarshal(body, &in) != nil {
		return g.refuse(http.StatusBadRequest, "invalid")
	}
	if v, ok := in["name"].(string); ok {
		gr.Name = v
	}
	if v, ok := in["description"].(string); ok {
		gr.Description = v
	}
	return answer{status: http.StatusOK, body: groupBody(gr)}
}

func (g *FakeGoogle) deleteGroup(key string) answer {
	gr := g.group(key)
	if gr == nil {
		return g.notFound()
	}
	delete(g.groups, gr.ID)
	return answer{status: http.StatusNoContent}
}

// listMembers is members.list, paged by maxResults with the offset as the
// page token.
func (g *FakeGoogle) listMembers(key string, q url.Values) answer {
	gr := g.group(key)
	if gr == nil {
		return g.notFound()
	}
	ids := slices.Sorted(maps.Keys(gr.Members))
	size, _ := strconv.Atoi(q.Get("maxResults"))
	if size <= 0 {
		size = 200
	}
	from, _ := strconv.Atoi(q.Get("pageToken"))
	body := map[string]any{"kind": "admin#directory#members"}
	var out []map[string]any
	for i := from; i < len(ids) && i < from+size; i++ {
		out = append(out, map[string]any{"kind": "admin#directory#member", "id": ids[i], "role": gr.Members[ids[i]], "type": "USER", "status": "ACTIVE"})
	}
	if len(out) > 0 {
		body["members"] = out
	}
	if from+size < len(ids) {
		body["nextPageToken"] = strconv.Itoa(from + size)
	}
	return answer{status: http.StatusOK, body: body}
}

func (g *FakeGoogle) insertMember(key string, body []byte) answer {
	gr := g.group(key)
	if gr == nil {
		return g.notFound()
	}
	var in struct {
		ID   string `json:"id"`
		Role string `json:"role"`
	}
	if json.Unmarshal(body, &in) != nil || in.ID == "" {
		return g.refuse(http.StatusBadRequest, "required")
	}
	if _, ok := g.users[in.ID]; !ok {
		return g.refuse(http.StatusNotFound, "notFound")
	}
	if _, ok := gr.Members[in.ID]; ok {
		return g.refuse(http.StatusConflict, "duplicate")
	}
	if in.Role == "" {
		in.Role = "MEMBER"
	}
	gr.Members[in.ID] = in.Role
	return answer{status: http.StatusOK, body: map[string]any{"id": in.ID, "role": in.Role, "type": "USER"}}
}

func (g *FakeGoogle) patchMember(key, member string, body []byte) answer {
	gr := g.group(key)
	if gr == nil {
		return g.notFound()
	}
	if _, ok := gr.Members[member]; !ok {
		return g.notFound()
	}
	var in struct {
		Role string `json:"role"`
	}
	if json.Unmarshal(body, &in) != nil || in.Role == "" {
		return g.refuse(http.StatusBadRequest, "required")
	}
	gr.Members[member] = in.Role
	return answer{status: http.StatusOK, body: map[string]any{"id": member, "role": in.Role, "type": "USER"}}
}

func (g *FakeGoogle) deleteMember(key, member string) answer {
	gr := g.group(key)
	if gr == nil {
		return g.notFound()
	}
	if _, ok := gr.Members[member]; !ok {
		return g.notFound()
	}
	delete(gr.Members, member)
	return answer{status: http.StatusNoContent}
}

// groupSettings is Groups Settings' patch of the group whose address is
// key.
func (g *FakeGoogle) groupSettings(key string, body []byte) answer {
	email, _ := url.PathUnescape(key)
	gr := g.group(email)
	if gr == nil {
		return g.notFound()
	}
	var in map[string]any
	if json.Unmarshal(body, &in) != nil {
		return g.refuse(http.StatusBadRequest, "invalid")
	}
	maps.Copy(gr.Settings, in)
	return answer{status: http.StatusOK, body: gr.Settings}
}

// licensingRoute is a licenseAssignments call: get, insert and delete of
// one user's licence, and listForProduct.
func (g *FakeGoogle) licensingRoute(req *http.Request, body []byte) (string, func() answer) {
	rest, ok := strings.CutPrefix(req.URL.Path, "/apps/licensing/v1/product/")
	if !ok {
		return "", nil
	}
	parts := strings.Split(rest, "/")
	unescape := func(s string) string { v, _ := url.PathUnescape(s); return v }
	switch {
	case len(parts) == 2 && parts[1] == "users" && req.Method == http.MethodGet:
		return ScopeLicensing, func() answer { return g.listForProduct(unescape(parts[0]), req.URL.Query()) }
	case len(parts) == 4 && parts[1] == "sku" && parts[3] == "user" && req.Method == http.MethodPost:
		licence := unescape(parts[0]) + "/" + unescape(parts[2])
		return ScopeLicensing, func() answer { return g.insertLicence(licence, body) }
	case len(parts) == 5 && parts[1] == "sku" && parts[3] == "user":
		licence := unescape(parts[0]) + "/" + unescape(parts[2])
		email := strings.ToLower(unescape(parts[4]))
		switch req.Method {
		case http.MethodGet:
			return ScopeLicensing, func() answer {
				if !g.assignments[licence][email] {
					return g.notFound()
				}
				return answer{status: http.StatusOK, body: map[string]any{"kind": "licensing#licenseAssignment", "userId": email}}
			}
		case http.MethodDelete:
			return ScopeLicensing, func() answer {
				if !g.assignments[licence][email] {
					return g.notFound()
				}
				delete(g.assignments[licence], email)
				return answer{status: http.StatusNoContent}
			}
		}
	}
	return "", nil
}

func (g *FakeGoogle) insertLicence(licence string, body []byte) answer {
	var in struct {
		UserID string `json:"userId"`
	}
	if json.Unmarshal(body, &in) != nil || in.UserID == "" {
		return g.refuse(http.StatusBadRequest, "required")
	}
	if n, ok := g.seats[licence]; ok {
		if n <= 0 {
			return g.refuse(http.StatusPreconditionFailed, "conditionNotMet")
		}
		g.seats[licence] = n - 1
	}
	g.assign(licence, in.UserID)
	return answer{status: http.StatusOK, body: map[string]any{"kind": "licensing#licenseAssignment", "userId": in.UserID}}
}

// listForProduct answers every assignment of product, for customerId the
// fake's customer.
func (g *FakeGoogle) listForProduct(product string, q url.Values) answer {
	if !slices.Contains(g.products, product) {
		return g.refuse(http.StatusBadRequest, "invalid")
	}
	if q.Get("customerId") != g.CustomerID {
		return g.refuse(http.StatusForbidden, "forbidden")
	}
	var items []map[string]any
	for _, licence := range slices.Sorted(maps.Keys(g.assignments)) {
		p, sku, _ := strings.Cut(licence, "/")
		if p != product {
			continue
		}
		for _, email := range slices.Sorted(maps.Keys(g.assignments[licence])) {
			items = append(items, map[string]any{"productId": p, "skuId": sku, "skuName": g.skuNames[licence], "userId": email})
		}
	}
	body := map[string]any{"kind": "licensing#licenseAssignmentList"}
	if len(items) > 0 {
		body["items"] = items
	}
	return answer{status: http.StatusOK, body: body}
}
