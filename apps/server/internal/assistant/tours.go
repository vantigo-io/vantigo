package assistant

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/vantigo-io/vantigo/server/internal/ai"
	"github.com/vantigo-io/vantigo/server/internal/assistant/gen"
)

// Guided tours (the Assistant design S11). The model never writes a selector:
// it names targets from a registry the module frontends declare, and the page
// finds each control by its data-assist attribute. `go generate` merges the
// frontends' registries into assist-targets.json (key, route and the English
// description); tours_test.go fails when that copy is stale.
//
//go:embed assist-targets.json
var assistTargetsJSON []byte

const (
	toolShowTour = "show_tour"

	maxTourSteps  = 8
	maxTourTitle  = 80
	maxTourBody   = 400
	tourTargetMax = 120
)

// tourTarget is one control the Assistant can point at.
type tourTarget struct {
	Key         string `json:"key"`
	Route       string `json:"route"`
	Description string `json:"description"`
}

type tourRegistry struct {
	list  []tourTarget
	byKey map[string]tourTarget
}

var (
	tourRegistryOnce sync.Once
	theTourRegistry  tourRegistry
)

// tourTargets is the embedded registry. A copy that does not parse is an empty
// registry (the tool is then not offered); the tests catch it long before.
func tourTargets() tourRegistry {
	tourRegistryOnce.Do(func() {
		var list []tourTarget
		if json.Unmarshal(assistTargetsJSON, &list) != nil {
			return
		}
		sort.Slice(list, func(i, j int) bool { return list[i].Key < list[j].Key })
		by := make(map[string]tourTarget, len(list))
		for _, t := range list {
			by[t.Key] = t
		}
		theTourRegistry = tourRegistry{list: list, byKey: by}
	})
	return theTourRegistry
}

// showTourTool is the show_tour declaration: the registry is its enum and is
// listed in the description, one line a target, so the model chooses and never
// invents.
func showTourTool(reg tourRegistry) ai.Tool {
	keys := make([]string, 0, len(reg.list))
	var b strings.Builder
	for _, t := range reg.list {
		keys = append(keys, t.Key)
		fmt.Fprintf(&b, "\n- %s (%s): %s", t.Key, t.Route, t.Description)
	}
	return ai.Tool{
		Name: toolShowTour,
		Description: "Offer the person a guided tour that points at controls on screen, one step at a time. It never starts on its own: the person clicks a \"Show me\" button under your answer. " +
			"Give the written steps first, then the tour. Each step is a target from the list below with a short title and a text in the person's language, naming the control as the interface does. " +
			"A target that opens a dialog (a form field, a Save button) is only on screen once the person has clicked the button before it, so say so in the text of that step. " +
			"The tour does not fill in or save anything. Use it only for a how-to the targets cover, at most 8 steps, in the order the person does them. Targets:" + b.String(),
		Parameters: objectSchema([]string{"steps"}, map[string]any{
			"steps": map[string]any{
				"type":        "array",
				"description": "The steps, in order.",
				"minItems":    1,
				"maxItems":    maxTourSteps,
				"items": map[string]any{
					"type":     "object",
					"required": []string{"target", "title", "body"},
					"properties": map[string]any{
						"target": map[string]any{"type": "string", "enum": keys, "description": "A target key from the list."},
						"title":  str("The step's heading, a few words, in the person's language."),
						"body":   str("One or two sentences saying what to do here, in the person's language."),
					},
				},
			},
		}),
	}
}

// toolDefs is every tool the model is offered: A's, and show_tour while the
// registry has targets.
var toolDefs = func() []ai.Tool {
	defs := append([]ai.Tool(nil), baseToolDefs...)
	if reg := tourTargets(); len(reg.list) > 0 {
		defs = append(defs, showTourTool(reg))
	}
	return defs
}()

// plainText reports whether s is non-empty text without control characters
// (a line break is allowed) and no longer than max runes.
func plainText(s string, max int) bool {
	s = strings.TrimSpace(s)
	if s == "" || !utf8.ValidString(s) || utf8.RuneCountInString(s) > max {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) && r != '\n' {
			return false
		}
	}
	return true
}

// showTour validates a tour against the registry and turns it into a part the
// panel draws as a "Show me" button. An unknown target refuses the whole call,
// so the model can try again with a registered one.
func (t *toolbox) showTour(raw json.RawMessage) outcome {
	var args struct {
		Steps []struct {
			Target string `json:"target"`
			Title  string `json:"title"`
			Body   string `json:"body"`
		} `json:"steps"`
	}
	if err := toolArgs(raw, &args); err != nil || len(args.Steps) == 0 {
		return refused("invalid_arguments", "steps is required: a list of {target, title, body}")
	}
	if len(args.Steps) > maxTourSteps {
		return refused("too_many_steps", fmt.Sprintf("a tour has at most %d steps; shorten it", maxTourSteps))
	}
	reg := tourTargets()
	steps := make([]gen.AssistantTourStep, 0, len(args.Steps))
	for i, s := range args.Steps {
		if _, ok := reg.byKey[s.Target]; !ok || len(s.Target) > tourTargetMax {
			return refused("unknown_target", fmt.Sprintf("step %d: that is not a registered target; use a key from the list in the tool's description, or leave the tour out", i+1))
		}
		if !plainText(s.Title, maxTourTitle) {
			return refused("invalid_arguments", fmt.Sprintf("step %d: title is required, plain text, at most %d characters", i+1, maxTourTitle))
		}
		if !plainText(s.Body, maxTourBody) {
			return refused("invalid_arguments", fmt.Sprintf("step %d: body is required, plain text, at most %d characters", i+1, maxTourBody))
		}
		steps = append(steps, gen.AssistantTourStep{Target: s.Target, Title: strings.TrimSpace(s.Title), Body: strings.TrimSpace(s.Body)})
	}
	return outcome{
		content:   wrap(map[string]any{"shown": fmt.Sprintf("a \"Show me\" button for a tour of %d steps; the person starts it with a click", len(steps))}),
		status:    "ok",
		mode:      "tool",
		operation: "tool:" + toolShowTour,
		tour:      steps,
	}
}
