package assistant

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/ai"
)

func TestSearchDocs_FindsTheTaskInThePersonsLanguage(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		query, lang, wantPrefix, wantHeading string
	}{
		{"how do I add a metering point", "en", "/docs/en/user/energy/", "Create a metering point"},
		{"hvordan legger jeg til et målepunkt", "nb", "/docs/nb/user/energy/", "Opprett et målepunkt"},
		{"overdue invoices reminder", "en", "/docs/en/user/invoices/", "Overdue invoices and reminders"},
		{"forfalte fakturaer purring", "nb", "/docs/nb/user/invoices/", "Forfalte fakturaer og purringer"},
		{"AI_CONTEXT_TOKENS tool calling", "en", "/docs/en/admin/", ""}, // from the administration guide, once documented
	} {
		got, err := searchDocs(tc.query, tc.lang, 4)
		if err != nil || len(got) == 0 {
			t.Fatalf("searchDocs(%q, %s) = %v, %v", tc.query, tc.lang, got, err)
		}
		found := false
		for _, r := range got {
			if strings.HasPrefix(r.Path, tc.wantPrefix) && (tc.wantHeading == "" || r.Heading == tc.wantHeading) {
				found = true
			}
			if r.Title == "" || r.Heading == "" || r.Excerpt == "" || !strings.HasPrefix(r.Path, "/docs/") {
				t.Errorf("searchDocs(%q): an incomplete result %+v", tc.query, r)
			}
		}
		if !found && tc.wantHeading != "" {
			t.Errorf("searchDocs(%q, %s) = %+v, want a result under %s headed %q", tc.query, tc.lang, got, tc.wantPrefix, tc.wantHeading)
		}
	}
	// A language the index does not have is English; an empty query finds nothing.
	if got, _ := searchDocs("metering point", "sv", 2); len(got) == 0 || !strings.HasPrefix(got[0].Path, "/docs/en/") {
		t.Errorf("an unknown language = %+v, want English", got)
	}
	if got, _ := searchDocs("the of and", "en", 4); len(got) != 0 {
		t.Errorf("a query of stop words found %+v", got)
	}
}

func TestSearchDocs_FallsBackToEnglishForWhatTheLanguageLacks(t *testing.T) {
	t.Parallel()
	got, err := searchDocs("zzzz-no-such-word metering point", "nb", 4)
	if err != nil || len(got) == 0 {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestSlugger_FollowsStarlight(t *testing.T) {
	t.Parallel()
	g := newSlugger()
	for heading, want := range map[string]string{
		"Create a metering point":      "create-a-metering-point",
		"Opprett et målepunkt":         "opprett-et-målepunkt",
		"A customer's reminder policy": "a-customers-reminder-policy",
		"What the assistant may use?":  "what-the-assistant-may-use",
		"AI (models) & keys":           "ai-models--keys",
		"Under_score and-hyphen":       "under_score-and-hyphen",
	} {
		if got := g.slug(heading); got != want {
			t.Errorf("slug(%q) = %q, want %q", heading, got, want)
		}
	}
	// A repeated heading is suffixed, as github-slugger does.
	if a, b, c := g.slug("Steps"), g.slug("Steps"), g.slug("Steps"); a != "steps" || b != "steps-1" || c != "steps-2" {
		t.Errorf("repeated = %s %s %s", a, b, c)
	}
}

func TestSplitPage_KeepsTheHeadingPathAndSkipsCodeFences(t *testing.T) {
	t.Parallel()
	page := "---\ntitle: Things\n---\nIntro text.\n\n## Make one\n\nSteps here.\n\n```\n## not a heading\n```\n\n### In detail\n\nMore [link](/en/x/) text.\n"
	secs := splitPage("en", "user", "things", page)
	if len(secs) != 3 {
		t.Fatalf("sections = %d, want intro, Make one, In detail", len(secs))
	}
	if secs[0].heading != "Things" || secs[0].anchor != "" || secs[0].text != "Intro text." {
		t.Errorf("intro = %+v", secs[0])
	}
	if secs[1].anchor != "make-one" || !strings.Contains(secs[1].text, "Steps here.") || strings.Contains(secs[1].text, "```") {
		t.Errorf("section = %+v", secs[1])
	}
	if secs[2].headingPath != "Things › Make one › In detail" || secs[2].text != "More link text." {
		t.Errorf("subsection = %+v", secs[2])
	}
}

// The embedded copy is generated; this fails when it is stale, as the
// freshness check of `go generate` does in CI.
func TestEmbeddedDocsAreTheDocumentation(t *testing.T) {
	t.Parallel()
	source := filepath.Join("..", "..", "..", "..", "docs", "src", "content", "docs")
	for _, lang := range []string{"en", "nb"} {
		for _, section := range []string{"user", "admin"} {
			entries, err := os.ReadDir(filepath.Join(source, lang, section))
			if err != nil {
				t.Fatal(err)
			}
			embedded, err := fs.ReadDir(docsFS, "docs/"+lang+"/"+section)
			if err != nil {
				t.Fatal(err)
			}
			var want, got []string
			for _, e := range entries {
				if strings.HasSuffix(e.Name(), ".md") {
					want = append(want, e.Name())
					a, _ := os.ReadFile(filepath.Join(source, lang, section, e.Name()))
					b, err := fs.ReadFile(docsFS, "docs/"+lang+"/"+section+"/"+e.Name())
					if err != nil || string(a) != string(b) {
						t.Errorf("%s/%s/%s is stale in the embedded copy: run `go generate ./...` in apps/server", lang, section, e.Name())
					}
				}
			}
			for _, e := range embedded {
				got = append(got, e.Name())
			}
			if strings.Join(want, ",") != strings.Join(got, ",") {
				t.Errorf("%s/%s: pages %v embedded, %v in the docs: run `go generate ./...` in apps/server", lang, section, got, want)
			}
		}
	}
}

func TestValidRoute(t *testing.T) {
	t.Parallel()
	for route, want := range map[string]bool{
		"/customers":                  true,
		"/customers/123":              true,
		"/customers/123/invoices":     true,
		"/invoices/overdue?page=2":    true,
		"/energy/metering-points":     true,
		"/assistant":                  true,
		"":                            false,
		"customers":                   false,
		"//evil.example/customers":    false,
		"https://evil.example/":       false,
		"/customersevil":              false,
		"/nothing":                    false,
		"/customers/../admin":         false,
		"/customers\\evil":            false,
		"/customers/123 or something": false,
		"javascript:alert(1)":         false,
		"/workspace/users":            false, // administration is the person's own, not the assistant's to send them to
	} {
		if got := validRoute(route); got != want {
			t.Errorf("validRoute(%q) = %v, want %v", route, got, want)
		}
	}
}

func TestCompactJSON_TruncatesListsAndKeepsTheCursor(t *testing.T) {
	t.Parallel()
	items := make([]any, 100)
	for i := range items {
		items[i] = map[string]any{"id": i, "name": strings.Repeat("name ", 20)}
	}
	got := compactJSON(map[string]any{"items": items, "nextCursor": "abc", "total": 100}, 2000)
	raw, _ := json.Marshal(got)
	if len(raw) > 2000 {
		t.Fatalf("compacted to %d bytes, want at most 2000", len(raw))
	}
	m := got.(map[string]any)
	if m["nextCursor"] != "abc" || m["total"] != 100 {
		t.Errorf("the cursor and the count must survive: %s", raw)
	}
	if shown, _ := m["items_shown_of"].(string); !strings.HasSuffix(shown, " of 100") {
		t.Errorf("a truncated list must say how many there are: %s", raw)
	}
	if long := compactValue(strings.Repeat("x", 1000), 5, 0).(string); len([]rune(long)) > maxStringChars+1 {
		t.Errorf("a long string kept %d characters", len([]rune(long)))
	}
}

func TestFitContext_SummarisesOldToolResultsThenDropsOldTurns(t *testing.T) {
	t.Parallel()
	big := strings.Repeat("r", 4000) // 1000 tokens
	history := []ai.Message{
		{Role: ai.RoleUser, Text: "first question"},
		{Role: ai.RoleAssistant, ToolCalls: []ai.ToolCall{{ID: "1", Name: "call_operation", Arguments: json.RawMessage(`{}`)}}},
		{Role: ai.RoleTool, ToolCallID: "1", Text: big},
		{Role: ai.RoleAssistant, Text: "first answer"},
		{Role: ai.RoleUser, Text: "second question"},
		{Role: ai.RoleAssistant, ToolCalls: []ai.ToolCall{{ID: "2", Name: "call_operation", Arguments: json.RawMessage(`{}`)}}},
		{Role: ai.RoleTool, ToolCallID: "2", Text: big},
	}
	// Under the budget nothing changes.
	if got := fitContext("sys", nil, history, 100000); got[2].Text != big || len(got) != len(history) {
		t.Error("a conversation under the budget must be left alone")
	}
	// Past it, the older result is summarised and the current one kept.
	got := fitContext("sys", nil, history, 1500)
	if got[2].Text == big || !strings.Contains(got[2].Text, "left out") || got[6].Text != big {
		t.Errorf("results = %q … %q, want the old one summarised and the latest kept", got[2].Text, got[6].Text[:10])
	}
	// Past that, earlier turns go, never the latest question.
	got = fitContext(strings.Repeat("s", 4000), nil, history, 1005)
	if got[0].Role != ai.RoleUser || got[0].Text != "second question" {
		t.Errorf("first message = %+v, want the second question after dropping the first turn", got[0])
	}
	if history[2].Text != big {
		t.Error("fitContext must not change its input")
	}
}

func TestTruncateTitle(t *testing.T) {
	t.Parallel()
	if got := truncateTitle("  How do I\nadd a metering point?  "); got != "How do I add a metering point?" {
		t.Errorf("got %q", got)
	}
	long := strings.Repeat("word ", 40)
	got := truncateTitle(long)
	if len([]rune(got)) > titleFromQuestion+1 || !strings.HasSuffix(got, "…") {
		t.Errorf("long title %q", got)
	}
}

func TestParseVersion(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]int32{"3": 3, `"3"`: 3, `W/"4"`: 4, " 0 ": 0} {
		if got, ok := parseVersion(in); !ok || got != want {
			t.Errorf("parseVersion(%q) = %d, %v", in, got, ok)
		}
	}
	for _, in := range []string{"", "x", "-1", `"`} {
		if _, ok := parseVersion(in); ok {
			t.Errorf("parseVersion(%q) accepted", in)
		}
	}
}
