package identity

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/config"
)

// recordingLogger returns a logger whose JSON records land in the buffer.
func recordingLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})), &buf
}

func logRecords(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var records []map[string]any
	for _, line := range bytes.Split(buf.Bytes(), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal(line, &rec); err != nil {
			t.Fatalf("log line %q: %v", line, err)
		}
		records = append(records, rec)
	}
	return records
}

// Ported from BootstrapSecretProviderTests.MissingSecret_InDevelopment_GeneratesUrlSafeHighEntropySecretAndLogsWarning.
func TestBootstrapSecret_MissingInDevelopmentIsGeneratedAndLoggedAsAWarning(t *testing.T) {
	logger, buf := recordingLogger()
	secret := resolveBootstrapSecret(&config.Config{Env: config.Development}, logger)

	if len(secret) < 43 || strings.ContainsAny(secret, "+/=") {
		t.Errorf("secret %q: want at least 43 URL-safe characters without padding", secret)
	}
	if raw, err := base64.RawURLEncoding.DecodeString(secret); err != nil || len(raw) != 32 {
		t.Errorf("secret %q decodes to %d bytes (%v), want 32", secret, len(raw), err)
	}
	records := logRecords(t, buf)
	if len(records) != 1 {
		t.Fatalf("logged %d records, want exactly one: %v", len(records), records)
	}
	rec := records[0]
	if rec["level"] != "WARN" || rec["msg"] != "bootstrap secret generated" || rec["bootstrap_secret"] != secret {
		t.Errorf("record = %v, want a WARN \"bootstrap secret generated\" carrying the secret", rec)
	}
	if note, _ := rec["note"].(string); !strings.Contains(note, "valid only until setup completes or this process restarts") {
		t.Errorf("note = %q", note)
	}

	if again := resolveBootstrapSecret(&config.Config{Env: config.Development}, logger); again == secret {
		t.Error("two resolutions generated the same secret")
	}
}

// Ported from BootstrapSecretProviderTests.MissingSecret_OutsideDevelopment_ThrowsAndNeverLogs.
// Go's startup failure is config.Load's; the environment below is otherwise
// incomplete on purpose, since Load reports every problem at once.
func TestBootstrapSecret_MissingOutsideDevelopmentFailsConfigurationAndNeverLogs(t *testing.T) {
	for _, v := range []string{"", "   "} {
		_, err := config.Load(map[string]string{"APP_ENV": "production", "BOOTSTRAP_SECRET": v})
		if err == nil || !strings.Contains(err.Error(), "BOOTSTRAP_SECRET: is required outside development") {
			t.Errorf("BOOTSTRAP_SECRET=%q: error = %v, want it required", v, err)
		}
	}
	if _, err := config.Load(map[string]string{"APP_ENV": "production", "BOOTSTRAP_SECRET": "set"}); err != nil && strings.Contains(err.Error(), "BOOTSTRAP_SECRET") {
		t.Errorf("with a secret set: error = %v, still names BOOTSTRAP_SECRET", err)
	}

	// Were an empty secret ever to reach it, the resolver neither generates
	// nor logs outside development, and "" matches no supplied secret.
	logger, buf := recordingLogger()
	if got := resolveBootstrapSecret(&config.Config{Env: config.Production}, logger); got != "" || buf.Len() != 0 {
		t.Errorf("production without a secret: got %q, logged %q", got, buf.String())
	}
	if secretMatches("", "") || secretMatches("", "anything") {
		t.Error("an empty configured secret matched")
	}
}

// Ported from BootstrapSecretProviderTests.ConfiguredSecret_IsUsedExactlyAndNeverLogged.
func TestBootstrapSecret_ConfiguredIsUsedExactlyAndNeverLogged(t *testing.T) {
	const configured = " configured-secret-with-preserved-space "
	for _, env := range []config.Env{config.Development, config.Production} {
		t.Run(string(env), func(t *testing.T) {
			logger, buf := recordingLogger()
			if got := resolveBootstrapSecret(&config.Config{Env: env, BootstrapSecret: configured}, logger); got != configured {
				t.Errorf("secret = %q, want %q verbatim", got, configured)
			}
			if buf.Len() != 0 {
				t.Errorf("logged %q, want nothing", buf.String())
			}
			if !secretMatches(configured, configured) || secretMatches(configured, strings.TrimSpace(configured)) {
				t.Error("the secret must match its exact bytes and nothing else")
			}
		})
	}
}
