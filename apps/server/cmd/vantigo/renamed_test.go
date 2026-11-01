package main

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/config"
)

// TestLogRenamedVariables_NamesThemOnceAndNeverTheirValues pins the boot line
// for variables that were renamed: one warning naming each old variable, its
// replacement and whether it is ignored, and no line at all when only current
// names are in use. The key is never in it.
func TestLogRenamedVariables_NamesThemOnceAndNeverTheirValues(t *testing.T) {
	var out bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&out, nil))

	logRenamedVariables(logger, &config.Config{})
	if out.Len() != 0 {
		t.Fatalf("logged %q for a configuration with no renamed variable, want nothing", out.String())
	}

	logRenamedVariables(logger, &config.Config{
		AIAPIKey: "sk-secret-value",
		Renamed: []config.RenamedVariable{
			{Old: "COMMUNICATIONS_AI_ENABLED", New: "AI_PROVIDER"},
			{Old: "COMMUNICATIONS_AI_API_KEY", New: "AI_API_KEY", Ignored: true},
		},
	})
	line := out.String()
	if strings.Count(line, "\n") != 1 {
		t.Fatalf("logged %d lines, want one: %q", strings.Count(line, "\n"), line)
	}
	for _, want := range []string{"COMMUNICATIONS_AI_ENABLED -> AI_PROVIDER", "COMMUNICATIONS_AI_API_KEY -> AI_API_KEY (ignored)", `"level":"WARN"`} {
		if !strings.Contains(line, want) {
			t.Errorf("line %q lacks %q", line, want)
		}
	}
	if strings.Contains(line, "sk-secret-value") {
		t.Errorf("line %q holds the key", line)
	}
}
