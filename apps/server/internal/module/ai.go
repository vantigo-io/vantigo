package module

import "github.com/vantigo-io/vantigo/server/internal/ai"

// withAI resolves contracts.AIModels from the enabled modules — at most one
// may declare it — and builds Deps.AI over it from Config, keeping a preset
// value of either when it has nothing of its own to put there (the seams
// modtest.WithAIModels fills). Compose calls it after Deps.AITasks is set;
// Workers does not call it.
func withAI(deps Deps, enabled []Module) (Deps, error) {
	provider, err := soleProvider(enabled, "AI models", func(m Module) bool { return m.AIModels != nil })
	if err != nil {
		return deps, err
	}
	if provider != nil {
		deps.AIModels = provider.AIModels(deps)
	}
	if deps.AI == nil {
		// ai.New answers nil unless a provider, a key and a model are
		// configured, which is the unavailable state.
		deps.AI = ai.New(deps.Config, deps.HTTPTransport, deps.AIModels, ai.WithLogger(deps.Logger))
	}
	return deps, nil
}

// withAssistantGrants resolves contracts.AssistantGrants from the enabled
// modules — at most one may declare it — keeping a preset value when none
// does. Compose calls it after the AI slot; Workers does not: a grant is
// issued while the person's own message request is being served.
func withAssistantGrants(deps Deps, enabled []Module) (Deps, error) {
	provider, err := soleProvider(enabled, "assistant grants", func(m Module) bool { return m.AssistantGrants != nil })
	if err != nil {
		return deps, err
	}
	if provider != nil {
		deps.AssistantGrants = provider.AssistantGrants(deps)
	}
	return deps, nil
}
