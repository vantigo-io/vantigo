package agents_test

import (
	"bytes"
	"image"
	"image/png"
	"net/http"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

func TestPermissions_ViewListsManageChangesAndNothingElseDoes(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	nobody := h.SignIn(t)
	viewer := h.SignIn(t, "agents:view")
	mgr := manager(t, h)

	if r := nobody.Do(http.MethodGet, "/api/v1/agents/agents", nil); r.Status != http.StatusForbidden {
		t.Errorf("list with no permission = %d, want 403", r.Status)
	}
	if r := viewer.Do(http.MethodGet, "/api/v1/agents/agents", nil); r.Status != http.StatusOK {
		t.Errorf("list with agents:view = %d %s, want 200", r.Status, r.Body)
	}
	if r := viewer.Do(http.MethodPost, "/api/v1/agents/agents", personaBody("Hanna")); r.Status != http.StatusForbidden {
		t.Errorf("create with agents:view = %d, want 403", r.Status)
	}
	if r := viewer.Do(http.MethodPut, "/api/v1/agents/settings", map[string]any{"companyName": "X", "retentionMonths": 6}, ifMatch(1)); r.Status != http.StatusForbidden {
		t.Errorf("settings change with agents:view = %d, want 403", r.Status)
	}
	setCompany(t, mgr, "Hansen Rør")
	a := createAgent(t, mgr, "Hanna")
	if r := viewer.Do(http.MethodPost, "/api/v1/agents/agents/"+a.ID+"/pause", nil); r.Status != http.StatusForbidden {
		t.Errorf("pause with agents:view = %d, want 403", r.Status)
	}
	if r := viewer.Do(http.MethodGet, "/api/v1/agents/agents/"+a.ID, nil); r.Status != http.StatusOK {
		t.Errorf("get with agents:view = %d, want 200", r.Status)
	}
}

func TestSettings_TheCompanyAndTheRetentionChangeUnderIfMatch(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	mgr := manager(t, h)

	s := getSettings(t, mgr)
	if s.CompanyName != "" || s.RetentionMonths != 6 || s.Version != 1 || !s.Configured || len(s.Models) != 2 || s.Models[0] != "test-model" {
		t.Fatalf("defaults = %+v", s)
	}
	for name, body := range map[string]map[string]any{
		"an empty company":     {"companyName": "  ", "retentionMonths": 6},
		"a retention of 0":     {"companyName": "Hansen Rør", "retentionMonths": 0},
		"a retention of 61":    {"companyName": "Hansen Rør", "retentionMonths": 61},
		"a company of 121 chr": {"companyName": strings.Repeat("x", 121), "retentionMonths": 6},
	} {
		if r := mgr.Do(http.MethodPut, "/api/v1/agents/settings", body, ifMatch(1), modtest.SkipContract("a body the contract refuses before the handler sees it")); r.Status != http.StatusBadRequest {
			t.Errorf("%s = %d %s, want 400", name, r.Status, r.Body)
		}
	}
	if r := mgr.Do(http.MethodPut, "/api/v1/agents/settings", map[string]any{"companyName": "Hansen Rør", "retentionMonths": 12}, modtest.Header("If-Match", "latest")); r.Status != http.StatusBadRequest {
		t.Errorf("a bad If-Match = %d, want 400", r.Status)
	}
	r := mgr.Do(http.MethodPut, "/api/v1/agents/settings", map[string]any{"companyName": " Hansen Rør ", "retentionMonths": 12}, ifMatch(1))
	if r.Status != http.StatusOK {
		t.Fatalf("change: %d %s", r.Status, r.Body)
	}
	var changed settings
	r.JSON(&changed)
	if changed.CompanyName != "Hansen Rør" || changed.RetentionMonths != 12 || changed.Version != 2 {
		t.Errorf("changed = %+v", changed)
	}
	if r := mgr.Do(http.MethodPut, "/api/v1/agents/settings", map[string]any{"companyName": "Other", "retentionMonths": 6}, ifMatch(1)); r.Status != http.StatusConflict {
		t.Errorf("stale If-Match = %d, want 409", r.Status)
	}
	if n := h.Count(t, `SELECT count(*) FROM agents.events WHERE action = 'settings.changed'`); n != 1 {
		t.Errorf("settings events = %d, want 1", n)
	}
}

func TestCreate_NeedsTheCompanyNameAndMakesAnAgentUser(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	mgr := manager(t, h)

	r := mgr.Do(http.MethodPost, "/api/v1/agents/agents", personaBody("Hanna"))
	if r.Status != http.StatusConflict || !strings.Contains(string(r.Body), "company_name_required") {
		t.Fatalf("create before the company's name = %d %s, want 409 company_name_required", r.Status, r.Body)
	}
	setCompany(t, mgr, "Hansen Rør")
	a := createAgent(t, mgr, "Hanna")
	if a.State != "active" || a.Name != "Hanna" || a.Model != "test-model" || a.HasAvatar || len(a.Roles) != 0 || a.Version != 1 {
		t.Errorf("created = %+v", a)
	}
	if n := h.Count(t, `SELECT count(*) FROM identity.users WHERE id = $1 AND kind = 'agent' AND display_name = 'Hanna' AND NOT is_disabled`, a.UserID); n != 1 {
		t.Errorf("the agent's user: %d rows, want one active user of kind agent", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM agents.events WHERE action = 'agent.created'`); n != 1 {
		t.Errorf("created events = %d, want 1", n)
	}

	r = mgr.Do(http.MethodGet, "/api/v1/agents/agents", nil)
	var list struct {
		Agents []agent `json:"agents"`
	}
	r.JSON(&list)
	if len(list.Agents) != 1 || list.Agents[0].ID != a.ID {
		t.Errorf("list = %s", r.Body)
	}
}

func TestCreate_RefusesWhatIsNotValid(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	mgr := manager(t, h)
	setCompany(t, mgr, "Hansen Rør")

	cases := map[string]func(map[string]any){
		"an empty name":         func(b map[string]any) { b["name"] = "  " },
		"a name of 61":          func(b map[string]any) { b["name"] = strings.Repeat("x", 61) },
		"a style of 1501":       func(b map[string]any) { b["style"] = strings.Repeat("x", 1501) },
		"a language":            func(b map[string]any) { b["languages"] = "de" },
		"a disclosure wording":  func(b map[string]any) { b["disclosureWording"] = "secret" },
		"a model not allowed":   func(b map[string]any) { b["model"] = "gpt-9" },
		"a style with too many": func(b map[string]any) { b["style"] = strings.Repeat("æ", 1501) },
	}
	for name, mutate := range cases {
		body := personaBody("Hanna")
		mutate(body)
		r := mgr.Do(http.MethodPost, "/api/v1/agents/agents", body, modtest.SkipContract("a value the contract's schema refuses before the handler"))
		if r.Status != http.StatusBadRequest {
			t.Errorf("%s = %d %s, want 400", name, r.Status, r.Body)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM identity.users WHERE kind = 'agent'`); n != 0 {
		t.Errorf("a refused create left %d agent users", n)
	}
}

func TestUpdate_ChangesThePersonaTheUserAndTheModelUnderIfMatch(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	mgr := manager(t, h)
	setCompany(t, mgr, "Hansen Rør")
	a := createAgent(t, mgr, "Hanna")

	body := map[string]any{"name": "Hanne", "style": "Formal.", "languages": "nb", "disclosureWording": "compact", "model": "other-model"}
	r := mgr.Do(http.MethodPut, "/api/v1/agents/agents/"+a.ID, body, ifMatch(a.Version))
	if r.Status != http.StatusOK {
		t.Fatalf("update: %d %s", r.Status, r.Body)
	}
	var got agent
	r.JSON(&got)
	if got.Name != "Hanne" || got.Style != "Formal." || got.Languages != "nb" || got.DisclosureWording != "compact" || got.Model != "other-model" || got.Version != a.Version+1 {
		t.Errorf("updated = %+v", got)
	}
	if n := h.Count(t, `SELECT count(*) FROM identity.users WHERE id = $1 AND display_name = 'Hanne'`, a.UserID); n != 1 {
		t.Errorf("the agent's user was not renamed")
	}
	if r := mgr.Do(http.MethodPut, "/api/v1/agents/agents/"+a.ID, body, ifMatch(a.Version)); r.Status != http.StatusConflict {
		t.Errorf("stale If-Match = %d, want 409", r.Status)
	}
	if r := mgr.Do(http.MethodPut, "/api/v1/agents/agents/"+newID(), body, ifMatch(1)); r.Status != http.StatusNotFound {
		t.Errorf("an unknown agent = %d, want 404", r.Status)
	}
	// Following the workspace again.
	body["model"] = ""
	r = mgr.Do(http.MethodPut, "/api/v1/agents/agents/"+a.ID, body, ifMatch(got.Version))
	r.JSON(&got)
	if got.Model != "test-model" {
		t.Errorf("model after forgetting the choice = %q, want the workspace's test-model", got.Model)
	}
}

func TestPauseResumeDelete(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	mgr := manager(t, h)
	setCompany(t, mgr, "Hansen Rør")
	a := createAgent(t, mgr, "Hanna")
	path := "/api/v1/agents/agents/" + a.ID

	var got agent
	mgr.Do(http.MethodPost, path+"/pause", nil).JSON(&got)
	if got.State != "paused" {
		t.Errorf("state after pause = %q", got.State)
	}
	mgr.Do(http.MethodPost, path+"/pause", nil).JSON(&got)
	if got.State != "paused" {
		t.Errorf("pausing twice = %q, want paused", got.State)
	}
	mgr.Do(http.MethodPost, path+"/resume", nil).JSON(&got)
	if got.State != "active" {
		t.Errorf("state after resume = %q", got.State)
	}
	if r := mgr.Do(http.MethodPost, "/api/v1/agents/agents/"+newID()+"/resume", nil); r.Status != http.StatusNotFound {
		t.Errorf("resume an unknown agent = %d, want 404", r.Status)
	}

	// A role bound to a channel, which deleting the agent frees.
	o := owner(t, h)
	rl := addRole(t, mgr, a.ID, "Front desk")
	channel := newID()
	if r := o.Do(http.MethodPut, rolePath(a, rl)+"/channels", map[string]any{"channelIds": []string{channel}}); r.Status != http.StatusOK {
		t.Fatalf("bind: %d %s", r.Status, r.Body)
	}

	if r := mgr.Do(http.MethodDelete, path, nil); r.Status != http.StatusNoContent {
		t.Fatalf("delete: %d %s", r.Status, r.Body)
	}
	if r := mgr.Do(http.MethodGet, path, nil); r.Status != http.StatusNotFound {
		t.Errorf("get after delete = %d, want 404", r.Status)
	}
	if r := mgr.Do(http.MethodDelete, path, nil); r.Status != http.StatusNotFound {
		t.Errorf("delete twice = %d, want 404", r.Status)
	}
	if n := h.Count(t, `SELECT count(*) FROM identity.users WHERE id = $1 AND is_disabled`, a.UserID); n != 1 {
		t.Errorf("the deleted agent's user is not disabled")
	}
	if n := h.Count(t, `SELECT count(*) FROM agents.role_channels`); n != 0 {
		t.Errorf("channels still bound after delete: %d", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM agents.agents WHERE id = $1 AND state = 'deleted'`, a.ID); n != 1 {
		t.Errorf("the row is not kept as deleted")
	}
	var list struct {
		Agents []agent `json:"agents"`
	}
	mgr.Do(http.MethodGet, "/api/v1/agents/agents", nil).JSON(&list)
	if len(list.Agents) != 0 {
		t.Errorf("list after delete = %d agents", len(list.Agents))
	}
	// The channel can be bound again by another agent.
	b := createAgent(t, mgr, "Henrik")
	rb := addRole(t, mgr, b.ID, "Front desk")
	if r := o.Do(http.MethodPut, rolePath(b, rb)+"/channels", map[string]any{"channelIds": []string{channel}}); r.Status != http.StatusOK {
		t.Errorf("rebind the freed channel: %d %s", r.Status, r.Body)
	}
}

func TestRoles_CreateUpdateAndRemove(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	mgr := manager(t, h)
	setCompany(t, mgr, "Hansen Rør")
	a := createAgent(t, mgr, "Hanna")

	rl := addRole(t, mgr, a.ID, "  Front desk ")
	if rl.Name != "Front desk" || rl.ReplyMode != "review" || rl.Threshold != 85 || rl.CapsToolCalls != 30 || rl.CapsTurns != 8 || rl.MaxRepliesPerDay != 5 ||
		rl.HandOffUserID != nil || rl.HandOffWarning != "none" || len(rl.ChannelIDs) != 0 || rl.Instructions == "" || rl.Template != "front_desk" {
		t.Fatalf("a new role = %+v", rl)
	}
	if got := getAgent(t, mgr, a.ID); len(got.Roles) != 1 || got.Version != a.Version+1 {
		t.Errorf("the agent after adding a role: %d roles, version %d (was %d)", len(got.Roles), got.Version, a.Version)
	}
	if r := mgr.Do(http.MethodPost, "/api/v1/agents/agents/"+newID()+"/roles", map[string]any{"template": "front_desk", "name": "X", "instructions": ""}); r.Status != http.StatusNotFound {
		t.Errorf("a role on an unknown agent = %d, want 404", r.Status)
	}
	if r := mgr.Do(http.MethodPost, "/api/v1/agents/agents/"+a.ID+"/roles", map[string]any{"template": "front_desk", "name": "", "instructions": strings.Repeat("x", 8001)}, modtest.SkipContract("a name the contract refuses")); r.Status != http.StatusBadRequest {
		t.Errorf("an invalid role = %d, want 400", r.Status)
	}

	body := roleBody(rl)
	body["name"], body["instructions"], body["capsTurns"] = "Reception", "Answer in short.", 4
	r := mgr.Do(http.MethodPut, rolePath(a, rl), body, ifMatch(rl.Version))
	if r.Status != http.StatusOK {
		t.Fatalf("update: %d %s", r.Status, r.Body)
	}
	var upd role
	r.JSON(&upd)
	if upd.Name != "Reception" || upd.Instructions != "Answer in short." || upd.CapsTurns != 4 || upd.Version != rl.Version+1 {
		t.Errorf("updated = %+v", upd)
	}
	if r := mgr.Do(http.MethodPut, rolePath(a, rl), body, ifMatch(rl.Version)); r.Status != http.StatusConflict {
		t.Errorf("stale If-Match = %d, want 409", r.Status)
	}
	for name, mutate := range map[string]func(map[string]any){
		"a threshold of 49":        func(b map[string]any) { b["threshold"] = 49 },
		"61 tool calls":            func(b map[string]any) { b["capsToolCalls"] = 61 },
		"17 turns":                 func(b map[string]any) { b["capsTurns"] = 17 },
		"51 replies a day":         func(b map[string]any) { b["maxRepliesPerDay"] = 51 },
		"a reply mode":             func(b map[string]any) { b["replyMode"] = "yolo" },
		"instructions of 8001 chr": func(b map[string]any) { b["instructions"] = strings.Repeat("æ", 8001) },
	} {
		bad := roleBody(upd)
		mutate(bad)
		if r := mgr.Do(http.MethodPut, rolePath(a, upd), bad, ifMatch(upd.Version), modtest.SkipContract("a value the contract's schema refuses before the handler")); r.Status != http.StatusBadRequest {
			t.Errorf("%s = %d %s, want 400", name, r.Status, r.Body)
		}
	}
	if r := mgr.Do(http.MethodPut, "/api/v1/agents/agents/"+a.ID+"/roles/"+newID(), body, ifMatch(1)); r.Status != http.StatusNotFound {
		t.Errorf("an unknown role = %d, want 404", r.Status)
	}

	if r := mgr.Do(http.MethodDelete, rolePath(a, rl), nil); r.Status != http.StatusNoContent {
		t.Fatalf("remove: %d %s", r.Status, r.Body)
	}
	if r := mgr.Do(http.MethodDelete, rolePath(a, rl), nil); r.Status != http.StatusNotFound {
		t.Errorf("remove twice = %d, want 404", r.Status)
	}
	if got := getAgent(t, mgr, a.ID); len(got.Roles) != 0 {
		t.Errorf("roles after remove = %d", len(got.Roles))
	}
}

func TestRoles_RaisingTheModeIsTheOwnersAlone(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	mgr := manager(t, h)
	o := owner(t, h)
	setCompany(t, mgr, "Hansen Rør")
	a := createAgent(t, mgr, "Hanna")
	rl := addRole(t, mgr, a.ID, "Front desk")

	auto := roleBody(rl)
	auto["replyMode"] = "auto_above"
	r := mgr.Do(http.MethodPut, rolePath(a, rl), auto, ifMatch(rl.Version))
	if r.Status != http.StatusForbidden || !strings.Contains(string(r.Body), "owner_required") {
		t.Fatalf("a manager choosing auto_above = %d %s, want 403 owner_required", r.Status, r.Body)
	}
	r = o.Do(http.MethodPut, rolePath(a, rl), auto, ifMatch(rl.Version))
	if r.Status != http.StatusOK {
		t.Fatalf("the Owner choosing auto_above = %d %s", r.Status, r.Body)
	}
	var cur role
	r.JSON(&cur)

	// Lowering the threshold leaves more to the agent too.
	lower := roleBody(cur)
	lower["threshold"] = 70
	if r := mgr.Do(http.MethodPut, rolePath(a, cur), lower, ifMatch(cur.Version)); r.Status != http.StatusForbidden {
		t.Errorf("a manager lowering the threshold = %d, want 403", r.Status)
	}
	// Going back to Review is anyone's with agents:manage.
	review := roleBody(cur)
	review["replyMode"] = "review"
	if r := mgr.Do(http.MethodPut, rolePath(a, cur), review, ifMatch(cur.Version)); r.Status != http.StatusOK {
		t.Errorf("a manager choosing review = %d %s, want 200", r.Status, r.Body)
	}
	if n := h.Count(t, `SELECT count(*) FROM agents.events WHERE action = 'role.mode.changed'`); n != 2 {
		t.Errorf("mode events = %d, want 2", n)
	}
}

func TestRoles_ChannelsAreTheOwnersToBindAndOneRolesEach(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	mgr := manager(t, h)
	o := owner(t, h)
	setCompany(t, mgr, "Hansen Rør")
	a := createAgent(t, mgr, "Hanna")
	b := createAgent(t, mgr, "Henrik")
	ra, rb := addRole(t, mgr, a.ID, "Front desk"), addRole(t, mgr, b.ID, "Front desk")
	c1, c2 := newID(), newID()
	bind := func(c *modtest.Client, a agent, r role, ids ...string) *modtest.Response {
		return c.Do(http.MethodPut, rolePath(a, r)+"/channels", map[string]any{"channelIds": ids})
	}

	if r := bind(mgr, a, ra, c1); r.Status != http.StatusForbidden || !strings.Contains(string(r.Body), "owner_required") {
		t.Fatalf("a manager binding = %d %s, want 403 owner_required", r.Status, r.Body)
	}
	r := bind(o, a, ra, c1, c2, c1)
	if r.Status != http.StatusOK {
		t.Fatalf("the Owner binding: %d %s", r.Status, r.Body)
	}
	var got role
	r.JSON(&got)
	if len(got.ChannelIDs) != 2 {
		t.Errorf("channels = %v, want the two, deduplicated", got.ChannelIDs)
	}
	// Binding what the role already has is no raise; unbinding is anyone's.
	if r := bind(mgr, a, ra, c1, c2); r.Status != http.StatusOK {
		t.Errorf("a manager keeping the channels = %d %s", r.Status, r.Body)
	}
	if r := bind(mgr, a, ra, c1); r.Status != http.StatusOK {
		t.Errorf("a manager unbinding = %d %s", r.Status, r.Body)
	}
	// Another role may not take a channel that is answered.
	if r := bind(o, b, rb, c1); r.Status != http.StatusConflict || !strings.Contains(string(r.Body), "channel_bound") {
		t.Errorf("a second role on the channel = %d %s, want 409 channel_bound", r.Status, r.Body)
	}
	if n := h.Count(t, `SELECT count(*) FROM agents.role_channels`); n != 1 {
		t.Errorf("bound channels = %d, want 1 (the refused bind rolled back)", n)
	}
	if r := bind(o, a, ra, c1, c2, newID()); r.Status != http.StatusOK {
		t.Errorf("rebinding: %d %s", r.Status, r.Body)
	}
	if r := o.Do(http.MethodPut, "/api/v1/agents/agents/"+a.ID+"/roles/"+newID()+"/channels", map[string]any{"channelIds": []string{}}); r.Status != http.StatusNotFound {
		t.Errorf("an unknown role = %d, want 404", r.Status)
	}
}

func TestRoles_TheHandOffPersonIsCheckedAndWarnedAbout(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	mgr := manager(t, h)
	setCompany(t, mgr, "Hansen Rør")
	a := createAgent(t, mgr, "Hanna")
	rl := addRole(t, mgr, a.ID, "Front desk")

	put := func(user any) *modtest.Response {
		body := roleBody(rl)
		body["handOffUserId"] = user
		return mgr.Do(http.MethodPut, rolePath(a, rl), body, ifMatch(getAgent(t, mgr, a.ID).Roles[0].Version))
	}
	if r := put(newID()); r.Status != http.StatusBadRequest {
		t.Errorf("an unknown hand-off person = %d, want 400", r.Status)
	}
	if r := put(a.UserID); r.Status != http.StatusBadRequest {
		t.Errorf("an agent as the hand-off person = %d, want 400", r.Status)
	}
	_, person := h.SignInUser(t, "communications:conversations-view")
	r := put(person.String())
	if r.Status != http.StatusOK {
		t.Fatalf("a person: %d %s", r.Status, r.Body)
	}
	var cur role
	r.JSON(&cur)
	if cur.HandOffWarning != "none" || cur.HandOffUserID == nil {
		t.Errorf("a person who may see conversations = %+v", cur)
	}
	// The person loses the permission, then is disabled.
	h.Exec(t, `DELETE FROM identity.role_permissions WHERE permission_key = 'communications:conversations-view'`)
	if w := getAgent(t, mgr, a.ID).Roles[0].HandOffWarning; w != "no_permission" {
		t.Errorf("warning without the permission = %q", w)
	}
	h.Exec(t, `UPDATE identity.users SET is_disabled = true WHERE id = $1`, person)
	if w := getAgent(t, mgr, a.ID).Roles[0].HandOffWarning; w != "disabled" {
		t.Errorf("warning for a disabled person = %q", w)
	}
}

func TestLevels_AnAgentHasNoneUntilTheOwnerRaisesThem(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	mgr := manager(t, h)
	o := owner(t, h)
	setCompany(t, mgr, "Hansen Rør")
	a := createAgent(t, mgr, "Hanna")
	setup := func() (ready bool, hasRoles bool, items []map[string]any) {
		r := mgr.Do(http.MethodGet, "/api/v1/agents/agents/"+a.ID+"/setup", nil)
		if r.Status != http.StatusOK {
			t.Fatalf("setup: %d %s", r.Status, r.Body)
		}
		var s struct {
			Ready    bool             `json:"ready"`
			HasRoles bool             `json:"hasRoles"`
			Items    []map[string]any `json:"items"`
		}
		r.JSON(&s)
		return s.Ready, s.HasRoles, s.Items
	}
	if ready, has, items := setup(); ready || has || len(items) != 0 {
		t.Errorf("setup of an agent with no role = ready %v, roles %v, %d items", ready, has, len(items))
	}
	addRole(t, mgr, a.ID, "Front desk")
	ready, has, items := setup()
	// Only the permissions of the modules that are enabled are needed: the
	// harness composes no communications, customers or products here.
	if ready || !has {
		t.Errorf("setup with a role = ready %v, roles %v", ready, has)
	}
	for _, it := range items {
		if it["level"] != "off" || it["satisfied"] != false {
			t.Errorf("a fresh agent's item = %v, want off and unsatisfied", it)
		}
	}

	perm := "/api/v1/agents/agents/" + a.ID + "/permissions/"
	if r := mgr.Do(http.MethodPut, perm+"agents:view", map[string]any{"level": "read"}); r.Status != http.StatusConflict || !strings.Contains(string(r.Body), "level_locked") {
		t.Errorf("a level for a locked permission = %d %s, want 409 level_locked", r.Status, r.Body)
	}
	if r := o.Do(http.MethodPut, perm+"identity:manage", map[string]any{"level": "read"}); r.Status != http.StatusConflict {
		t.Errorf("a level for identity:manage, even the Owner's = %d, want 409", r.Status)
	}
	if r := o.Do(http.MethodPut, perm+"widgets:view", map[string]any{"level": "read"}); r.Status != http.StatusNotFound {
		t.Errorf("a level for a permission the catalog lacks = %d, want 404", r.Status)
	}
	if r := o.Do(http.MethodPut, perm+"agents:manage", map[string]any{"level": "everything"}, modtest.SkipContract("a level the contract's schema refuses")); r.Status != http.StatusBadRequest && r.Status != http.StatusConflict {
		t.Errorf("an invalid level = %d, want 400", r.Status)
	}
	if r := mgr.Do(http.MethodPut, perm+"knowledge:articles", map[string]any{"level": "read"}); r.Status != http.StatusForbidden || !strings.Contains(string(r.Body), "owner_required") {
		t.Errorf("a manager raising a level = %d %s, want 403 owner_required", r.Status, r.Body)
	}
	r := o.Do(http.MethodPut, perm+"knowledge:articles", map[string]any{"level": "change"})
	if r.Status != http.StatusOK {
		t.Fatalf("the Owner raising a level: %d %s", r.Status, r.Body)
	}
	var lv struct {
		PermissionKey string `json:"permissionKey"`
		Level         string `json:"level"`
		Version       int    `json:"version"`
	}
	r.JSON(&lv)
	if lv.PermissionKey != "knowledge:articles" || lv.Level != "change" || lv.Version != getAgent(t, mgr, a.ID).Version {
		t.Errorf("level answer = %+v, want change at the agent's version", lv)
	}
	// Lowering is anyone's with agents:manage; off forgets the row.
	if r := mgr.Do(http.MethodPut, perm+"knowledge:articles", map[string]any{"level": "read"}); r.Status != http.StatusOK {
		t.Errorf("a manager lowering a level = %d %s, want 200", r.Status, r.Body)
	}
	if r := mgr.Do(http.MethodPut, perm+"knowledge:articles", map[string]any{"level": "off"}); r.Status != http.StatusOK {
		t.Errorf("a manager turning a level off = %d %s, want 200", r.Status, r.Body)
	}
	if n := h.Count(t, `SELECT count(*) FROM agents.permission_levels`); n != 0 {
		t.Errorf("rows after off = %d, want 0", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM agents.events WHERE action = 'permission.changed'`); n != 3 {
		t.Errorf("level events = %d, want 3", n)
	}
	if r := mgr.Do(http.MethodPut, "/api/v1/agents/agents/"+newID()+"/permissions/knowledge:articles", map[string]any{"level": "off"}); r.Status != http.StatusNotFound {
		t.Errorf("an unknown agent = %d, want 404", r.Status)
	}
}

func TestAvatar_IsSetAndServedAndRefusedWhenItIsNotAPicture(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	mgr := manager(t, h)
	setCompany(t, mgr, "Hansen Rør")
	a := createAgent(t, mgr, "Hanna")
	path := "/api/v1/agents/agents/" + a.ID + "/avatar"

	if r := mgr.Do(http.MethodGet, path, nil); r.Status != http.StatusNotFound {
		t.Errorf("an agent with no picture = %d, want 404", r.Status)
	}
	var pic bytes.Buffer
	if err := png.Encode(&pic, image.NewRGBA(image.Rect(0, 0, 8, 8))); err != nil {
		t.Fatal(err)
	}
	ct, body := multipartAvatar("image/png", pic.Bytes())
	r := mgr.Do(http.MethodPut, path, nil, modtest.RawBody(ct, body))
	if r.Status != http.StatusOK {
		t.Fatalf("upload: %d %s", r.Status, r.Body)
	}
	var got agent
	r.JSON(&got)
	if !got.HasAvatar {
		t.Errorf("hasAvatar after the upload = false")
	}
	g := mgr.Do(http.MethodGet, path, nil)
	if g.Status != http.StatusOK || g.Header("Content-Type") != "image/png" || g.Header("X-Content-Type-Options") != "nosniff" || !bytes.Equal(g.Body, pic.Bytes()) {
		t.Errorf("get = %d %s, %d bytes", g.Status, g.Header("Content-Type"), len(g.Body))
	}
	ct, body = multipartAvatar("image/png", []byte("not a picture"))
	if r := mgr.Do(http.MethodPut, path, nil, modtest.RawBody(ct, body)); r.Status != http.StatusBadRequest {
		t.Errorf("a file that is not a picture = %d, want 400", r.Status)
	}
	ct, body = multipartAvatar("image/png", pic.Bytes())
	if r := mgr.Do(http.MethodPut, "/api/v1/agents/agents/"+newID()+"/avatar", nil, modtest.RawBody(ct, body)); r.Status != http.StatusNotFound {
		t.Errorf("a picture for an unknown agent = %d, want 404", r.Status)
	}
}

func TestTemplates_ListTheFrontDesk(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	v := h.SignIn(t, "agents:view")
	r := v.Do(http.MethodGet, "/api/v1/agents/templates", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("templates: %d %s", r.Status, r.Body)
	}
	var out struct {
		Templates []struct {
			Key       string   `json:"key"`
			Allowlist []string `json:"allowlist"`
			Needs     []struct {
				PermissionKey string `json:"permissionKey"`
				Level         string `json:"level"`
			} `json:"needs"`
			HardRules []struct {
				Code   string `json:"code"`
				Locked bool   `json:"locked"`
			} `json:"hardRules"`
		} `json:"templates"`
	}
	r.JSON(&out)
	if len(out.Templates) != 1 || out.Templates[0].Key != "front_desk" || len(out.Templates[0].Allowlist) < 10 || len(out.Templates[0].Needs) < 10 {
		t.Fatalf("templates = %s", r.Body)
	}
	locked := map[string]bool{}
	for _, hr := range out.Templates[0].HardRules {
		locked[hr.Code] = hr.Locked
	}
	for _, code := range []string{"S1", "S2", "S6", "S7", "C1"} {
		if !locked[code] {
			t.Errorf("hard rule %s is not locked", code)
		}
	}
	for _, code := range []string{"S3", "S4", "S5", "C2"} {
		if locked[code] {
			t.Errorf("hard rule %s is locked", code)
		}
	}
}
