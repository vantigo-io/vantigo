package contracts_test

import (
	"slices"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// TestAPINever_IdentityAgentsAndTheOwnerTier proves K7 of the API access
// design: identity's and the agents' keys, and every permission that cannot be
// delegated, are never an API key's, and nothing else is.
func TestAPINever_IdentityAgentsAndTheOwnerTier(t *testing.T) {
	catalog := []contracts.Permission{
		{Key: "customers:view", Delegable: true},
		{Key: "customers:personal-data", Delegable: true},
		{Key: "entra:manage", Delegable: false},
		{Key: "identity:manage", Delegable: true},
		{Key: "agents:view", Delegable: true},
		{Key: "orders:settings", Delegable: false},
		{Key: "orders:settings", Delegable: false},
	}
	got := contracts.APINever(catalog)
	want := []string{"agents:view", "entra:manage", "identity:manage", "orders:settings"}
	if !slices.Equal(got, want) {
		t.Errorf("APINever = %v, want %v", got, want)
	}
}
