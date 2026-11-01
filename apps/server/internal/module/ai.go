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
		deps.AI = ai.New(deps.Config, deps.HTTPTransport, deps.AIModels)
	}
	return deps, nil
}
