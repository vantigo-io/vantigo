package contracts

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// AITask is one job a module does with a model (AI as a platform provider
// design T12). A module declares the tasks it performs the way it declares
// permissions — module.Module.AITasks — and Compose collects the enabled
// modules' into module.Deps.AITasks, a map by Key, so a disabled module's tasks
// vanish with its permissions. A task is declared by the delivery that performs
// it: a task nothing calls is a row on the AI models page that does nothing.
//
// The module calls ai.Client.Complete with Key and never with a model; which
// model that is is an Owner's choice per task (AIModels), among the models the
// instance's plan allows.
type AITask struct {
	// Key is "<module>.<task>", lower case, such as
	// "communications.compose-reply". It is the primary key of the Owner's
	// choice, so it never changes once shipped.
	Key string
	// Module is the declaring module's name; the page groups tasks by it.
	Module string
	// Display is what the task is called on the AI models page, in English,
	// such as "Draft a reply".
	Display string
	// Description says what the model is asked to do, and with what data.
	Description string
	// Default is the model the module recommends for the task; "" means the
	// plan's default. It applies only while the Owner has not chosen and only
	// when the plan allows it.
	Default string
}

var aiTaskKey = regexp.MustCompile(`^[a-z][a-z0-9]*\.[a-z][a-z0-9]*(-[a-z0-9]+)*$`)

// ValidateAITask reports why t, declared by module, is not a valid task:
// a key not of the form module.task-name whose module part is the declaring
// module, a Module that is not it, or a blank display or description.
func ValidateAITask(module string, t AITask) error {
	if !aiTaskKey.MatchString(t.Key) {
		return fmt.Errorf("contracts: AI task key %q is not of the form module.task-name", t.Key)
	}
	prefix, _, _ := strings.Cut(t.Key, ".")
	if prefix != module {
		return fmt.Errorf("contracts: AI task key %q does not start with module %q", t.Key, module)
	}
	if t.Module != module {
		return fmt.Errorf("contracts: AI task %q names module %q, want %q", t.Key, t.Module, module)
	}
	if strings.TrimSpace(t.Display) == "" {
		return fmt.Errorf("contracts: AI task %q needs a non-blank display", t.Key)
	}
	if strings.TrimSpace(t.Description) == "" {
		return fmt.Errorf("contracts: AI task %q needs a non-blank description", t.Key)
	}
	return nil
}

// AIModels is the model chosen for each AI task: identity's table of an
// Owner's choices read through a short cache, resolved against the models the
// instance allows (AI_MODELS). A single-provider slot, filled through
// module.Module.AIModels; Compose resolves the enabled module's into the
// resolver of the ai.Client it puts on Deps.AI, refusing two. A module never
// calls it: it calls ai.Client.Complete with a task key.
type AIModels interface {
	// ModelFor answers the model task is to use, always one of AI_MODELS:
	// the Owner's choice if there is one and it is still allowed, else the
	// task's Default if allowed, else the first of AI_MODELS. An error is a
	// failure to read the choice, never "no model".
	ModelFor(ctx context.Context, task string) (string, error)
}
