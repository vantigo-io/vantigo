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

// withAgentSlots resolves contracts.AgentAccounts and contracts.AgentGrants
// from the enabled modules — at most one may declare each — keeping a preset
// value when none does. Compose calls it beside withAssistantGrants; Workers
// does not: an agent's user and grant are made while a request or a run's
// claim is being served.
func withAgentSlots(deps Deps, enabled []Module) (Deps, error) {
	accounts, err := soleProvider(enabled, "agent accounts", func(m Module) bool { return m.AgentAccounts != nil })
	if err != nil {
		return deps, err
	}
	if accounts != nil {
		deps.AgentAccounts = accounts.AgentAccounts(deps)
	}
	grants, err := soleProvider(enabled, "agent grants", func(m Module) bool { return m.AgentGrants != nil })
	if err != nil {
		return deps, err
	}
	if grants != nil {
		deps.AgentGrants = grants.AgentGrants(deps)
	}
	return deps, nil
}
