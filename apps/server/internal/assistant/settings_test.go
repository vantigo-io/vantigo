package assistant_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

type settings struct {
	Mode             string   `json:"mode"`
	RetentionMonths  int      `json:"retentionMonths"`
	SensitiveDefault string   `json:"sensitiveDefault"`
	Version          int32    `json:"version"`
	Configured       bool     `json:"configured"`
	Provider         string   `json:"provider"`
	Local            bool     `json:"local"`
	Models           []string `json:"models"`
}

func getSettings(t *testing.T, c *modtest.Client) settings {
	t.Helper()
	r := c.Do(http.MethodGet, "/api/v1/assistant/settings", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("get the settings: %d %s", r.Status, r.Body)
	}
	var s settings
	r.JSON(&s)
	return s
}

func ifMatch(v any) modtest.RequestOption { return modtest.Header("If-Match", fmt.Sprint(v)) }

func TestSettings_TheOwnerReadsAndChangesThemUnderIfMatch(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.NewFakeChatModel())
	o := owner(t, h)

	s := getSettings(t, o)
	if s.Mode != "all" || s.RetentionMonths != 12 || s.SensitiveDefault != "off" || s.Version != 1 || !s.Configured || s.Provider != "openai-compatible" || !s.Local || len(s.Models) != 1 || s.Models[0] != "test-model" {
		t.Fatalf("defaults = %+v", s)
	}

	r := o.Do(http.MethodPut, "/api/v1/assistant/settings", map[string]any{"retentionMonths": 24, "sensitiveDefault": "read"}, ifMatch(1))
	if r.Status != http.StatusOK {
		t.Fatalf("change: %d %s", r.Status, r.Body)
	}
	var changed settings
	r.JSON(&changed)
	if changed.Mode != "all" || changed.RetentionMonths != 24 || changed.SensitiveDefault != "read" || changed.Version != 2 {
		t.Errorf("changed = %+v, want the mode kept and the rest changed", changed)
	}

	// A stale version is a conflict, quoted or bare.
	if r := o.Do(http.MethodPut, "/api/v1/assistant/settings", map[string]any{"mode": "disabled"}, ifMatch(1)); r.Status != http.StatusConflict {
		t.Errorf("stale If-Match = %d %s, want 409", r.Status, r.Body)
	}
	if r := o.Do(http.MethodPut, "/api/v1/assistant/settings", map[string]any{"retentionMonths": 6}, ifMatch(`"2"`)); r.Status != http.StatusOK {
		t.Errorf("quoted If-Match = %d %s, want 200", r.Status, r.Body)
	}
	// Bad values are field errors; a bad If-Match is a 400.
	for name, body := range map[string]map[string]any{
		"mode":             {"mode": "everyone"},
		"retentionMonths":  {"retentionMonths": 61},
		"sensitiveDefault": {"sensitiveDefault": "change"},
	} {
		r := o.Do(http.MethodPut, "/api/v1/assistant/settings", body, ifMatch(3))
		if r.Status != http.StatusBadRequest || !strings.Contains(string(r.Body), name) {
			t.Errorf("bad %s = %d %s, want 400 naming the field", name, r.Status, r.Body)
		}
	}
	if r := o.Do(http.MethodPut, "/api/v1/assistant/settings", map[string]any{"mode": "all"}, ifMatch("soon")); r.Status != http.StatusBadRequest {
		t.Errorf("If-Match soon = %d, want 400", r.Status)
	}

	// Each change is audited with before and after.
	if n := modtest.One[int](t, h, `SELECT count(*) FROM assistant.events WHERE action = 'settings.changed'`); n != 2 {
		t.Errorf("%d audit rows, want 2", n)
	}
	before := modtest.One[string](t, h, `SELECT before::text FROM assistant.events ORDER BY id LIMIT 1`)
	after := modtest.One[string](t, h, `SELECT after::text FROM assistant.events ORDER BY id LIMIT 1`)
	if !strings.Contains(before, `"retentionMonths": 12`) || !strings.Contains(after, `"retentionMonths": 24`) || !strings.Contains(after, `"sensitiveDefault": "read"`) {
		t.Errorf("audit before %s after %s", before, after)
	}
}

func TestSettings_OnlyAnOwnerMayUseTheOwnerEndpoints(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.NewFakeChatModel())
	c, id := person(t, h)
	for _, tc := range []struct {
		method, path string
		body         any
		opts         []modtest.RequestOption
	}{
		{http.MethodGet, "/api/v1/assistant/settings", nil, nil},
		{http.MethodPut, "/api/v1/assistant/settings", map[string]any{"mode": "disabled"}, []modtest.RequestOption{ifMatch(1)}},
		{http.MethodGet, "/api/v1/assistant/settings/permissions", nil, nil},
		{http.MethodPut, "/api/v1/assistant/settings/permissions/assistant:none", map[string]any{"level": "off"}, []modtest.RequestOption{ifMatch(1)}},
		{http.MethodGet, "/api/v1/assistant/people/" + id.String(), nil, nil},
		{http.MethodPut, "/api/v1/assistant/people/" + id.String(), map[string]any{"enabled": true}, []modtest.RequestOption{ifMatch(0)}},
	} {
		if r := c.Do(tc.method, tc.path, tc.body, tc.opts...); r.Status != http.StatusForbidden {
			t.Errorf("%s %s as a person = %d, want 403", tc.method, tc.path, r.Status)
		}
	}
	// Nor can the Owner read a person's conversations: there is no endpoint.
	conv := startConversation(t, c)
	o := owner(t, h)
	if r := o.Do(http.MethodGet, "/api/v1/assistant/conversations/"+conv.ID, nil); r.Status != http.StatusNotFound {
		t.Errorf("an Owner reading a person's conversation = %d, want 404", r.Status)
	}
	if r := o.Do(http.MethodGet, "/api/v1/assistant/conversations", nil); r.Status != http.StatusOK || strings.Contains(string(r.Body), conv.ID) {
		t.Errorf("an Owner's list = %d %s, want none of the person's", r.Status, r.Body)
	}
}

type permissionsDoc struct {
	Version int32 `json:"version"`
	Groups  []struct {
		Module      string          `json:"module"`
		Category    string          `json:"category"`
		Permissions []permissionRow `json:"permissions"`
	} `json:"groups"`
}

type permissionRow struct {
	Key            string `json:"key"`
	Display        string `json:"display"`
	Sensitive      bool   `json:"sensitive"`
	DefaultLevel   string `json:"defaultLevel"`
	Override       string `json:"override"`
	EffectiveLevel string `json:"effectiveLevel"`
}

func findPermission(t *testing.T, d permissionsDoc, key string) permissionRow {
	t.Helper()
	for _, g := range d.Groups {
		for _, p := range g.Permissions {
			if p.Key == key {
				return p
			}
		}
	}
	t.Fatalf("permission %s is not in the table", key)
	return permissionRow{}
}

func getPermissions(t *testing.T, c *modtest.Client) permissionsDoc {
	t.Helper()
	r := c.Do(http.MethodGet, "/api/v1/assistant/settings/permissions", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("permissions: %d %s", r.Status, r.Body)
	}
	var d permissionsDoc
	r.JSON(&d)
	return d
}

func TestPermissions_LevelsDefaultsOverridesAndReset(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.NewFakeChatModel())
	o := owner(t, h)

	d := getPermissions(t, o)
	if len(d.Groups) == 0 {
		t.Fatal("the table is empty")
	}
	// identity:manage is sensitive in the catalog: off by default.
	sensitive := findPermission(t, d, "identity:manage")
	if !sensitive.Sensitive || sensitive.DefaultLevel != "off" || sensitive.EffectiveLevel != "off" || sensitive.Override != "" {
		t.Errorf("identity:manage = %+v, want sensitive and off", sensitive)
	}
	for _, g := range d.Groups {
		for _, p := range g.Permissions {
			if !p.Sensitive && p.DefaultLevel != "change" {
				t.Errorf("%s: a permission that is not sensitive defaults to %s, want change", p.Key, p.DefaultLevel)
			}
		}
	}

	// The switch moves the sensitive defaults.
	r := o.Do(http.MethodPut, "/api/v1/assistant/settings", map[string]any{"sensitiveDefault": "read"}, ifMatch(d.Version))
	if r.Status != http.StatusOK {
		t.Fatalf("switch: %d %s", r.Status, r.Body)
	}
	d = getPermissions(t, o)
	if p := findPermission(t, d, "identity:manage"); p.DefaultLevel != "read" || p.EffectiveLevel != "read" {
		t.Errorf("after the switch identity:manage = %+v, want read", p)
	}

	// An override wins, and survives a change of the switch.
	set := func(key string, level any, version int32) *modtest.Response {
		return o.Do(http.MethodPut, "/api/v1/assistant/settings/permissions/"+key, map[string]any{"level": level}, ifMatch(version))
	}
	r = set("identity:manage", "change", d.Version)
	if r.Status != http.StatusOK {
		t.Fatalf("override: %d %s", r.Status, r.Body)
	}
	var res struct {
		Version    int32         `json:"version"`
		Permission permissionRow `json:"permission"`
	}
	r.JSON(&res)
	if res.Permission.Override != "change" || res.Permission.EffectiveLevel != "change" || res.Version != d.Version+1 {
		t.Errorf("override result = %+v", res)
	}
	r = o.Do(http.MethodPut, "/api/v1/assistant/settings", map[string]any{"sensitiveDefault": "off"}, ifMatch(res.Version))
	if r.Status != http.StatusOK {
		t.Fatalf("switch back: %d %s", r.Status, r.Body)
	}
	d = getPermissions(t, o)
	if p := findPermission(t, d, "identity:manage"); p.EffectiveLevel != "change" || p.DefaultLevel != "off" || p.Override != "change" {
		t.Errorf("an override must survive the switch: %+v", p)
	}

	// Reset with null.
	if r := set("identity:manage", nil, d.Version); r.Status != http.StatusOK {
		t.Fatalf("reset: %d %s", r.Status, r.Body)
	}
	d = getPermissions(t, o)
	if p := findPermission(t, d, "identity:manage"); p.Override != "" || p.EffectiveLevel != "off" {
		t.Errorf("after the reset = %+v, want the default", p)
	}

	// 404 for a key not in the catalog, 409 for a stale version, 400 for a bad level.
	if r := set("nothing:here", "off", d.Version); r.Status != http.StatusNotFound {
		t.Errorf("unknown key = %d, want 404", r.Status)
	}
	if r := set("identity:manage", "off", d.Version-1); r.Status != http.StatusConflict {
		t.Errorf("stale version = %d, want 409", r.Status)
	}
	if r := set("identity:manage", "write", d.Version); r.Status != http.StatusBadRequest {
		t.Errorf("bad level = %d, want 400", r.Status)
	}

	// Audited with before and after.
	if n := modtest.One[int](t, h, `SELECT count(*) FROM assistant.events WHERE action = 'permission.changed' AND target = 'identity:manage'`); n != 2 {
		t.Errorf("%d permission audit rows, want 2 (the override and the reset)", n)
	}
	first := modtest.One[string](t, h, `SELECT after::text FROM assistant.events WHERE action = 'permission.changed' ORDER BY id LIMIT 1`)
	if !strings.Contains(first, `"level": "change"`) {
		t.Errorf("the override's audit after = %s", first)
	}
}

func TestPeople_TheSwitchIsGuardedByIfMatchAndAudited(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.NewFakeChatModel())
	o := owner(t, h)
	_, id := person(t, h)

	r := o.Do(http.MethodGet, "/api/v1/assistant/people/"+id.String(), nil)
	var p struct {
		UserID  string `json:"userId"`
		Enabled bool   `json:"enabled"`
		Version int32  `json:"version"`
	}
	r.JSON(&p)
	if r.Status != http.StatusOK || p.Enabled || p.Version != 0 {
		t.Fatalf("a person with no row = %d %s, want off at version 0", r.Status, r.Body)
	}
	if r := o.Do(http.MethodPut, "/api/v1/assistant/people/"+id.String(), map[string]any{"enabled": true}, ifMatch(5)); r.Status != http.StatusConflict {
		t.Errorf("wrong version = %d, want 409", r.Status)
	}
	r = o.Do(http.MethodPut, "/api/v1/assistant/people/"+id.String(), map[string]any{"enabled": true}, ifMatch(0))
	r.JSON(&p)
	if r.Status != http.StatusOK || !p.Enabled || p.Version != 1 {
		t.Fatalf("switch on = %d %s", r.Status, r.Body)
	}
	if r := o.Do(http.MethodPut, "/api/v1/assistant/people/"+id.String(), map[string]any{"enabled": false}, ifMatch(0)); r.Status != http.StatusConflict {
		t.Errorf("stale = %d, want 409", r.Status)
	}
	if r := o.Do(http.MethodGet, "/api/v1/assistant/people/00000000-0000-4000-8000-0000000000ff", nil); r.Status != http.StatusNotFound {
		t.Errorf("an unknown user = %d, want 404", r.Status)
	}
	before := modtest.One[string](t, h, `SELECT before::text FROM assistant.events WHERE action = 'person.changed'`)
	after := modtest.One[string](t, h, `SELECT after::text FROM assistant.events WHERE action = 'person.changed'`)
	if !strings.Contains(before, `false`) || !strings.Contains(after, `true`) {
		t.Errorf("person audit before %s after %s", before, after)
	}
}
