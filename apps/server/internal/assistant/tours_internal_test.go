package assistant

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/ai"
	"github.com/vantigo-io/vantigo/server/internal/assistant/gen"
)

// The embedded registry is generated from the module frontends' own files
// (apps/*/frontend/src/assist-targets.json); this fails when it is stale, as
// the freshness check of `go generate` does in CI.
func TestEmbeddedTourTargetsAreTheFrontendsRegistries(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob(filepath.Join("..", "..", "..", "*", "frontend", "src", "assist-targets.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no frontend registries found: %v", err)
	}
	var want []tourTarget
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var list []struct {
			Key         string `json:"key"`
			Route       string `json:"route"`
			Description struct {
				En string `json:"en"`
			} `json:"description"`
		}
		if err := json.Unmarshal(raw, &list); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		for _, e := range list {
			want = append(want, tourTarget{Key: e.Key, Route: e.Route, Description: e.Description.En})
		}
	}
	sort.Slice(want, func(i, j int) bool { return want[i].Key < want[j].Key })

	var got []tourTarget
	if err := json.Unmarshal(assistTargetsJSON, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("the embedded registry has %d targets, the frontends %d: run `go generate ./...` in apps/server", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("target %d is stale in the embedded copy: have %+v, the frontends say %+v: run `go generate ./...` in apps/server", i, got[i], want[i])
		}
	}
}

var targetKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9-]*\.[a-z][a-z0-9-]*\.[a-z][a-z0-9-]*$`)

func TestTourRegistry_KeysAreWellFormedAndUnique(t *testing.T) {
	t.Parallel()
	reg := tourTargets()
	if len(reg.list) < 25 {
		t.Fatalf("%d targets, want the 25-40 the delivery marks", len(reg.list))
	}
	seen := map[string]bool{}
	for _, target := range reg.list {
		if !targetKeyPattern.MatchString(target.Key) || seen[target.Key] || !strings.HasPrefix(target.Route, "/") || strings.TrimSpace(target.Description) == "" {
			t.Errorf("bad target %+v", target)
		}
		seen[target.Key] = true
	}
}

func TestShowTourTool_ListsTheRegistryAsItsEnum(t *testing.T) {
	t.Parallel()
	tool := showTourTool(tourTargets())
	var schema struct {
		Properties struct {
			Steps struct {
				MaxItems int `json:"maxItems"`
				Items    struct {
					Properties struct {
						Target struct {
							Enum []string `json:"enum"`
						} `json:"target"`
					} `json:"properties"`
				} `json:"items"`
			} `json:"steps"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(tool.Parameters, &schema); err != nil {
		t.Fatal(err)
	}
	enum := schema.Properties.Steps.Items.Properties.Target.Enum
	if len(enum) != len(tourTargets().list) || schema.Properties.Steps.MaxItems != 8 {
		t.Errorf("enum %d, maxItems %d", len(enum), schema.Properties.Steps.MaxItems)
	}
	for _, target := range tourTargets().list {
		if !strings.Contains(tool.Description, target.Key) {
			t.Errorf("%s is not in the description", target.Key)
		}
	}
	found := false
	for _, def := range toolDefs {
		found = found || def.Name == toolShowTour
	}
	if !found {
		t.Error("show_tour is not among the tools the model is offered")
	}
	var _ ai.Tool = tool
}

func runShowTour(args string) outcome {
	return (&toolbox{}).showTour(json.RawMessage(args))
}

func TestShowTour_AnUnknownTargetRefusesTheWholeCall(t *testing.T) {
	t.Parallel()
	good := `{"target":"energy.metering-points.new","title":"Start","body":"Click New metering point."}`
	for name, args := range map[string]string{
		"unknown":       `{"steps":[` + good + `,{"target":"energy.metering-points.nope","title":"x","body":"y"}]}`,
		"a selector":    `{"steps":[{"target":"[data-assist] , body","title":"x","body":"y"}]}`,
		"empty target":  `{"steps":[{"target":"","title":"x","body":"y"}]}`,
		"a record key":  `{"steps":[{"target":"customers.customers.new ","title":"x","body":"y"}]}`,
		"no steps":      `{"steps":[]}`,
		"not an object": `[1]`,
	} {
		out := runShowTour(args)
		if out.status != "refused" || len(out.tour) != 0 {
			t.Errorf("%s: status %q, tour %v, want a refusal", name, out.status, out.tour)
		}
	}
	if out := runShowTour(`{"steps":[{"target":"energy.metering-points.nope","title":"x","body":"y"}]}`); !strings.Contains(out.content, "unknown_target") {
		t.Errorf("the refusal does not say why: %s", out.content)
	}
}

func TestShowTour_AtMostEightSteps(t *testing.T) {
	t.Parallel()
	step := `{"target":"energy.metering-points.new","title":"Step","body":"Do it."}`
	eight := `{"steps":[` + strings.TrimSuffix(strings.Repeat(step+",", 8), ",") + `]}`
	nine := `{"steps":[` + strings.TrimSuffix(strings.Repeat(step+",", 9), ",") + `]}`
	if out := runShowTour(eight); out.status != "ok" || len(out.tour) != 8 {
		t.Errorf("eight steps: %q, %d", out.status, len(out.tour))
	}
	out := runShowTour(nine)
	if out.status != "refused" || !strings.Contains(out.content, "too_many_steps") {
		t.Errorf("nine steps: %q %s", out.status, out.content)
	}
}

func TestShowTour_TheStepsAreTypedAndTextIsPlain(t *testing.T) {
	t.Parallel()
	out := runShowTour(`{"steps":[{"target":"energy.metering-points.new","title":"  Start  ","body":"Click New metering point."}]}`)
	if out.status != "ok" || len(out.tour) != 1 {
		t.Fatalf("outcome = %+v", out)
	}
	want := gen.AssistantTourStep{Target: "energy.metering-points.new", Title: "Start", Body: "Click New metering point."}
	if out.tour[0] != want {
		t.Errorf("step = %+v, want %+v", out.tour[0], want)
	}
	for name, args := range map[string]string{
		"no title":      `{"steps":[{"target":"energy.metering-points.new","title":" ","body":"y"}]}`,
		"no body":       `{"steps":[{"target":"energy.metering-points.new","title":"x","body":""}]}`,
		"control chars": `{"steps":[{"target":"energy.metering-points.new","title":"x\u0000","body":"y"}]}`,
		"long title":    `{"steps":[{"target":"energy.metering-points.new","title":"` + strings.Repeat("x", 81) + `","body":"y"}]}`,
		"long body":     `{"steps":[{"target":"energy.metering-points.new","title":"x","body":"` + strings.Repeat("x", 401) + `"}]}`,
	} {
		if got := runShowTour(args); got.status != "refused" {
			t.Errorf("%s: status %q, want a refusal", name, got.status)
		}
	}
}
