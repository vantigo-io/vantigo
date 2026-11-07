package meta_test

import (
	"bytes"
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/meta"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The settings (Messenger design N10): the Owner's, refused without a public
// host, the secret sealed, the verify token generated and regenerated, and the
// Human Agent switch read back by the provider's capabilities.

func settingsProvider(h *harness) contracts.ChannelMessenger {
	return meta.Module().ChannelMessenger(h.Deps())
}

func TestSettings_AreTheOwnersAlone(t *testing.T) {
	h := newHarness(t)
	plain := h.SignIn(t, "communications:channels-manage")
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, settingsPath},
		{http.MethodPut, settingsPath},
		{http.MethodPost, verifyTokenPath},
		{http.MethodPost, pagesPath},
		{http.MethodDelete, pagesPath + "/1"},
	} {
		var body any
		if tc.method == http.MethodPut || tc.path == pagesPath {
			body = map[string]any{"accessToken": "x"}
		}
		if r := plain.Do(tc.method, tc.path, body); r.Status != http.StatusForbidden {
			t.Errorf("%s %s as a non-Owner: status %d, want 403", tc.method, tc.path, r.Status)
		}
		if r := h.Client(t).Do(tc.method, tc.path, body); r.Status != http.StatusUnauthorized {
			t.Errorf("%s %s signed out: status %d, want 401", tc.method, tc.path, r.Status)
		}
	}
}

func TestSettings_StartWithAVerifyTokenAndTheWebhookAddress(t *testing.T) {
	h := newHarness(t)
	o, _ := owner(t, h)
	s := getSettings(t, o)
	if len(s.VerifyToken) != 64 || s.AppSecretSet || s.AppId != nil || s.HumanAgentGranted {
		t.Fatalf("settings at the start: %+v", s)
	}
	if !s.PublicHostConfigured || s.WebhookUrl == nil || *s.WebhookUrl != publicBase+"/api/v1/meta/webhook" {
		t.Fatalf("the webhook address: %+v", s)
	}
	if len(s.Pages) != 0 {
		t.Fatalf("pages at the start: %+v", s.Pages)
	}
}

func TestSettings_SaveTheAppAndSealTheSecret(t *testing.T) {
	h := newHarness(t)
	o, _ := owner(t, h)
	s := configure(t, o)
	if s.AppId == nil || *s.AppId != appID || !s.AppSecretSet {
		t.Fatalf("settings after saving: %+v", s)
	}
	if r := o.Do(http.MethodGet, settingsPath, nil); bytes.Contains(r.Body, []byte(appSecret)) {
		t.Fatalf("the app secret was answered: %s", r.Body)
	}
	var sealed []byte
	row := h.Pool().QueryRow(context.Background(), `SELECT app_secret_sealed FROM meta.settings WHERE id = 1`)
	if err := row.Scan(&sealed); err != nil || len(sealed) == 0 || bytes.Contains(sealed, []byte(appSecret)) {
		t.Fatalf("the stored secret: %v, plaintext inside: %v", err, bytes.Contains(sealed, []byte(appSecret)))
	}
	opened, err := h.Deps().Secrets.Open("meta/app-secret", sealed)
	if err != nil || string(opened) != appSecret {
		t.Fatalf("the secret opened under meta/app-secret: %q %v", opened, err)
	}

	// A field left out keeps its value; an empty secret keeps the stored one.
	r := o.Do(http.MethodPut, settingsPath, map[string]any{"humanAgentGranted": true, "appSecret": ""})
	if r.Status != http.StatusOK {
		t.Fatalf("PUT: %d %s", r.Status, r.Body)
	}
	s = getSettings(t, o)
	if s.AppId == nil || *s.AppId != appID || !s.AppSecretSet || !s.HumanAgentGranted {
		t.Fatalf("settings after a partial save: %+v", s)
	}
}

func TestSettings_AreRefusedWithoutAPublicHost(t *testing.T) {
	h := newHarness(t, modtest.WithEnv("PUBLIC_BASE_URL", ""))
	o, _ := owner(t, h)
	s := getSettings(t, o)
	if s.PublicHostConfigured || s.WebhookUrl != nil {
		t.Fatalf("settings without a public host: %+v", s)
	}
	r := o.Do(http.MethodPut, settingsPath, map[string]any{"appId": appID, "appSecret": appSecret})
	if r.Status != http.StatusUnprocessableEntity || problemCode(t, r) != "public_host_required" {
		t.Fatalf("PUT without a public host: %d %s", r.Status, r.Body)
	}
	if getSettings(t, o).AppId != nil {
		t.Fatal("a refused save changed the settings")
	}
}

func TestSettings_ValidateTheFields(t *testing.T) {
	h := newHarness(t)
	o, _ := owner(t, h)
	for name, body := range map[string]map[string]any{
		"an app id with letters": {"appId": "abc"},
		"an empty app id":        {"appId": " "},
		"a long app id":          {"appId": "123456789012345678901234567890123456789012"},
		"a long secret":          {"appSecret": string(bytes.Repeat([]byte("s"), 201))},
	} {
		if r := o.Do(http.MethodPut, settingsPath, body); r.Status != http.StatusBadRequest {
			t.Errorf("%s: status %d %s, want 400", name, r.Status, r.Body)
		}
	}
}

func TestSettings_RegenerateTheVerifyToken(t *testing.T) {
	h := newHarness(t)
	o, _ := owner(t, h)
	before := getSettings(t, o).VerifyToken
	r := o.Do(http.MethodPost, verifyTokenPath, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("POST verify-token: %d %s", r.Status, r.Body)
	}
	after := getSettings(t, o).VerifyToken
	if after == before || len(after) != 64 {
		t.Fatalf("verify token %q -> %q", before, after)
	}
}

func TestProvider_StatusAndCapabilities(t *testing.T) {
	h := newHarness(t)
	o, _ := owner(t, h)
	p := settingsProvider(h)
	if p.Key() != "meta" || p.DisplayName() != "Facebook Messenger" {
		t.Fatalf("Key %q DisplayName %q", p.Key(), p.DisplayName())
	}
	st, err := p.Status(context.Background())
	if err != nil || st.Available || st.Reason != "not_configured" {
		t.Fatalf("Status before the app is set: %+v %v", st, err)
	}
	configure(t, o)
	st, err = p.Status(context.Background())
	if err != nil || !st.Available || st.Reason != "" {
		t.Fatalf("Status after: %+v %v", st, err)
	}

	caps := p.Capabilities()
	if caps.MaxText != 2000 || caps.MaxAnswerOptions != 13 || caps.MaxAnswerTitle != 20 || !caps.ShareEmail ||
		caps.MaxCards != 10 || caps.MaxCardChoices != 3 || caps.MaxCardTitle != 80 || caps.MaxCardSubtitle != 80 || caps.MaxChoiceTitle != 20 || !caps.PublicMedia || caps.MaxAttachmentBytes != 25<<20 || caps.Window != 24*time.Hour || caps.HumanAgentWindow != 0 {
		t.Fatalf("Capabilities: %+v", caps)
	}
	if !caps.Components() {
		t.Fatal("Messenger takes components")
	}
	if r := o.Do(http.MethodPut, settingsPath, map[string]any{"humanAgentGranted": true}); r.Status != http.StatusOK {
		t.Fatalf("PUT: %d %s", r.Status, r.Body)
	}
	if got := p.Capabilities().HumanAgentWindow; got != 7*24*time.Hour {
		t.Fatalf("the Human Agent window with the setting: %v", got)
	}
}

func TestSettings_ClearingHumanAgentClearsItsError(t *testing.T) {
	h := newHarness(t)
	o, _ := owner(t, h)
	configure(t, o)
	o.Do(http.MethodPut, settingsPath, map[string]any{"humanAgentGranted": true})
	h.Exec(t, `UPDATE meta.settings SET last_graph_error_code = 'human_agent_not_permitted', last_graph_error = 'no', last_graph_error_at = now() WHERE id = 1`)
	s := getSettings(t, o)
	if s.Status.LastGraphErrorCode == nil || *s.Status.LastGraphErrorCode != "human_agent_not_permitted" {
		t.Fatalf("status: %+v", s.Status)
	}
	// An unrelated change keeps the error; clearing the checkbox clears it.
	o.Do(http.MethodPut, settingsPath, map[string]any{"appId": appID})
	if getSettings(t, o).Status.LastGraphErrorCode == nil {
		t.Fatal("an unrelated save cleared the error")
	}
	o.Do(http.MethodPut, settingsPath, map[string]any{"humanAgentGranted": false})
	if getSettings(t, o).Status.LastGraphErrorCode != nil {
		t.Fatal("clearing the checkbox kept the error")
	}
}
