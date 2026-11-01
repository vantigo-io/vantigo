package contracts

import "testing"

func TestValidateAITask(t *testing.T) {
	ok := AITask{Key: "communications.compose-reply", Module: "communications", Display: "Draft a reply", Description: "Drafts a reply from the conversation."}
	if err := ValidateAITask("communications", ok); err != nil {
		t.Fatalf("a valid task: %v", err)
	}
	for name, mutate := range map[string]func(*AITask){
		"a key with no task":      func(t *AITask) { t.Key = "communications" },
		"a key with a second dot": func(t *AITask) { t.Key = "communications.a.b" },
		"a key in upper case":     func(t *AITask) { t.Key = "communications.Compose" },
		"a key with a trailing -": func(t *AITask) { t.Key = "communications.compose-" },
		"another module's key":    func(t *AITask) { t.Key = "staff.compose-reply" },
		"another Module":          func(t *AITask) { t.Module = "staff" },
		"a blank display":         func(t *AITask) { t.Display = "  " },
		"a blank description":     func(t *AITask) { t.Description = "" },
	} {
		task := ok
		mutate(&task)
		if err := ValidateAITask("communications", task); err == nil {
			t.Errorf("%s: accepted, want it refused", name)
		}
	}
}
